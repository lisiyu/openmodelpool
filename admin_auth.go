package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"os"
	"sync"
)

// trustedReverseProxy is true when the deployment opts in via OMP_TRUSTED_PROXY=1,
// i.e. it sits behind a reverse proxy that terminates TLS and sets X-Forwarded-Proto.
// Header-based scheme detection must not be trusted otherwise: an attacker reaching
// the server directly can spoof the header to strip the Secure flag from cookies (G124).
var trustedReverseProxy = os.Getenv("OMP_TRUSTED_PROXY") == "1"

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
	// Determine if Secure flag should be set.
	// X-Forwarded-Proto is only trusted when the deployment opts in via
	// OMP_TRUSTED_PROXY=1 (see trustedReverseProxy above). In direct-exposure
	// deployments an attacker can spoof this header to prevent the Secure
	// cookie flag from being set.
	isHTTPS := r.TLS != nil || (trustedReverseProxy && r.Header.Get("X-Forwarded-Proto") == "https")
	c := &http.Cookie{ // #nosec G124 -- Secure is intentionally dynamic (true on TLS, false on plain HTTP); X-Forwarded-Proto trust is gated by OMP_TRUSTED_PROXY
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
	// B9-2: a distinctive error here would confirm the admin address to an
	// attacker probing the endpoint (matching email → 400, wrong email → 200).
	// Return the same generic response and only log the real reason.
	if !auth.IsSMTPConfigured() {
		slog.Info("forgot-password requested but SMTP is not configured")
		writeJSON(w, 200, map[string]any{"success": true, "message": "如果邮箱已配置，重置链接已发送"})
		return
	}

	// Generate reset token
	token := auth.CreateResetToken()

	// Send email with reset link
	s := auth.GetSMTP()
	adminEmail := auth.GetEmail()

	// Build reset URL from configured public_url (trusted). SEC-B3-6: never
	// trust the request Host header — an attacker can forge it to point the
	// admin's reset link at a phishing origin. Without public_url we fall back
	// to the local listen address (loopback + configured port), which is only
	// correct for same-host admin access.
	resetURL := ""
	if pubURL := cfg.Get("public_url", ""); pubURL != "" {
		resetURL = pubURL + "/forgot-password"
	} else {
		port := cfg.Get("port", "8000")
		resetURL = fmt.Sprintf("http://127.0.0.1:%s/forgot-password", port)
	}

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