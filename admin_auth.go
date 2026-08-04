package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"sync"
)

// ============================================================
// Auth handlers
// ============================================================

func handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"initialized": auth.Initialized()})
}

var setupMu sync.Mutex

func handleSetup(w http.ResponseWriter, r *http.Request) {
	setupMu.Lock()
	defer setupMu.Unlock()

	if auth.Initialized() {
		writeError(w, 400, "admin already initialized")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := auth.SetupAdmin(body.Username, body.Password, body.Email); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "data": auth.AdminInfo()})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, 400, "username and password required")
		return
	}
	if len(body.Username) > 128 || len(body.Password) > 256 { // B9: input length limit
		writeError(w, 400, "input too long")
		return
	}
	if !auth.VerifyCredentials(body.Username, body.Password) {
		writeError(w, 401, "invalid credentials")
		return
	}
	accessToken, refreshToken := auth.CreateToken(body.Username, body.Remember)
	maxAge := 86400
	if body.Remember {
		maxAge = 7 * 86400
	}
	// Determine if Secure flag should be set
	// NOTE: X-Forwarded-Proto is only trustworthy when behind a trusted
	// reverse proxy. In direct-exposure deployments, an attacker can spoof
	// this header to prevent the Secure cookie flag from being set.
	isHTTPS := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	c := &http.Cookie{
		Name:     "admin_token",
		Path:     "/",
		Value:    accessToken,
		HttpOnly: true,
		MaxAge:   maxAge,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS,
	}
	http.SetCookie(w, c)
	writeJSON(w, 200, map[string]string{"access_token": accessToken, "refresh_token": refreshToken, "token_type": "bearer"})
}

func handleVerifyAuth(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token == "" {
		writeJSON(w, 401, map[string]any{"valid": false, "error": "no token provided"})
		return
	}
	username, err := auth.VerifyToken(token)
	if err != nil {
		// P0-1: Properly reject invalid tokens instead of always returning true
		writeJSON(w, 401, map[string]any{"valid": false, "error": "invalid or expired token"})
		return
	}
	writeJSON(w, 200, map[string]any{"valid": true, "username": username})
}

func handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		writeError(w, 400, "system not initialized")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Verify email matches admin email
	if body.Email == "" || body.Email != auth.GetEmail() {
		// Always return success to prevent email enumeration
		writeJSON(w, 200, map[string]any{"success": true, "message": "如果邮箱已配置，重置链接已发送"})
		return
	}

	// Check if SMTP is configured
	if !auth.IsSMTPConfigured() {
		writeError(w, 400, "邮件服务未配置，无法发送重置链接。请使用「重置密码」功能（通过 Proxy API Key）")
		return
	}

	// Generate reset token
	token := auth.CreateResetToken()

	// Send email with reset link
	s := auth.GetSMTP()
	adminEmail := auth.GetEmail()

	// Build reset URL from request
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	resetURL := fmt.Sprintf("%s://%s/forgot-password", scheme, r.Host)

	subject := "OpenModelPool Agent 密码重置"
	// S-6: Token is included in email body, NOT in URL. User copies token to reset page.
	msgBody := fmt.Sprintf("Subject: %s\r\nFrom: %s\r\nTo: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n"+
		"<h3>OpenModelPool Agent 密码重置</h3>"+
		"<p>点击下方链接进入密码重置页面（30 分钟内有效）：</p>"+
		`<p><a href="%s" style="padding:10px 20px;background:#6c63ff;color:white;text-decoration:none;border-radius:6px;">重置密码</a></p>`+
		"<p>或复制以下重置令牌粘贴到重置页面：</p>"+
		"<p style='font-size:18px;font-weight:bold;letter-spacing:2px;color:#333;'>%s</p>"+
		"<p style='word-break:break-all;color:#666;'>%s</p>"+
		"<p style='color:#999;font-size:12px;'>如非本人操作，请忽略此邮件。</p>",
		subject, s.FromEmail, adminEmail, resetURL, token, resetURL)

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	var smtpAuth smtp.Auth
	if s.Username != "" {
		smtpAuth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}

	var err error
	if s.UseTLS && s.Port == 465 {
		err = sendMailTLS(addr, smtpAuth, s.FromEmail, []string{adminEmail}, []byte(msgBody))
	} else {
		err = smtp.SendMail(addr, smtpAuth, s.FromEmail, []string{adminEmail}, []byte(msgBody))
	}

	if err != nil {
		slog.Error("failed to send reset email", "error", err)
		writeError(w, 500, "发送重置邮件失败")
		return
	}

	slog.Info("password reset email sent", "email", adminEmail)
	writeJSON(w, 200, map[string]any{"success": true, "message": "重置链接已发送到你的邮箱"})
}

func handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := auth.ResetPassword(body.Token, body.NewPassword); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "password reset"})
}

func handleVerifyResetToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if !auth.VerifyResetToken(body.Token) {
		writeError(w, 400, "invalid or expired reset token")
		return
	}
	writeJSON(w, 200, map[string]bool{"valid": true})
}