package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
)

// ============================================================
// Status handler
// ============================================================

func handleStatus(w http.ResponseWriter, r *http.Request) {
	configured := pm.GetConfigured()
	withKey := 0
	for _, p := range configured {
		hasKey := (p.APIKey != "" && p.APIKey != "your-api-key-here") || len(p.APIKeys) > 0
		if hasKey {
			withKey++
		}
	}
	writeJSON(w, 200, map[string]any{
		"status":  "running",
		"version": AppVersion,
		"providers": map[string]any{
			"enabled":      withKey,
			"total":        len(configured),
			"preset_total": len(presetProviders),
		},
		"models": len(pm.AllModels()),
	})
}

// handleSMTPTest sends a test email using the configured SMTP settings.
func handleSMTPTest(w http.ResponseWriter, r *http.Request) {
	if !auth.IsSMTPConfigured() {
		writeError(w, 400, "SMTP not configured")
		return
	}
	s := auth.GetSMTP()
	adminEmail := auth.GetEmail()
	if adminEmail == "" {
		writeError(w, 400, "Admin email not set")
		return
	}

	// Build email message
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: OpenModelPool Agent 测试邮件\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n这是一封来自 OpenModelPool Agent 的测试邮件。\r\n\r\n如果您收到此邮件，说明 SMTP 配置成功！", s.FromEmail, adminEmail))

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	smtpAuth := smtp.PlainAuth("", s.Username, s.Password, s.Host)

	var err error
	if s.UseTLS && s.Port == 465 {
		// Implicit TLS (port 465)
		err = sendMailTLS(addr, smtpAuth, s.FromEmail, []string{adminEmail}, msg)
	} else {
		// STARTTLS or plain
		err = smtp.SendMail(addr, smtpAuth, s.FromEmail, []string{adminEmail}, msg)
	}

	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "detail": "邮件发送失败"})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "测试邮件已发送至 " + adminEmail})
}

// sendMailTLS sends email using implicit TLS (for port 465).
func sendMailTLS(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	tlsConfig := &tls.Config{ServerName: strings.Split(addr, ":")[0], MinVersion: tls.VersionTLS12} // B14
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, strings.Split(addr, ":")[0])
	if err != nil {
		return err
	}
	defer c.Close()
	if a != nil {
		if err = c.Auth(a); err != nil {
			return err
		}
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err = c.Rcpt(addr); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	_, err = wc.Write(msg)
	if err != nil {
		return err
	}
	err = wc.Close()
	if err != nil {
		return err
	}
	return c.Quit()
}

// POST /api/auth/reset-with-code — reset password using an independent admin reset code.
// P0-2: Proxy API Key is NO LONGER used as the reset credential.
// Instead, a dedicated ResetCode is generated and stored in admin.json.
// The reset code can be generated via the "generate-reset-code" CLI command or
// from the admin panel when SMTP is unavailable.