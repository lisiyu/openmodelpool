package main

import "net/http"

// ============================================================
// SMTP handlers
// ============================================================

func handleSMTPStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"configured": auth.IsSMTPConfigured()})
}

func handleGetSMTPConfig(w http.ResponseWriter, r *http.Request) {
	s := auth.GetSMTP()
	if s.Password != "" {
		s.Password = "****"
	}
	writeJSON(w, 200, s)
}

func handleSaveSMTPConfig(w http.ResponseWriter, r *http.Request) {
	var s SMTPConfig
	if err := readJSON(w, r, &s); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if s.Port == 0 {
		s.Port = 587
	}
	auth.UpdateSMTP(s)
	writeJSON(w, 200, map[string]any{"success": true, "message": "SMTP config saved"})
}