package main

import "net/http"

// ============================================================
// Collaborator Registration API (public, no auth required)
// ============================================================

// GET /api/collaborator/check-key?key=xxx — validate guest key for registration
func handleCollaboratorCheckKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, 400, "key parameter required")
		return
	}
	valid := auth.ValidateGuestKeyForRegistration(key)
	writeJSON(w, 200, map[string]any{"valid": valid})
}

// POST /api/collaborator/register — register collaborator account
func handleCollaboratorRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		GuestKey string `json:"guest_key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" || body.GuestKey == "" {
		writeError(w, 400, "username, password and guest_key are required")
		return
	}
	if len(body.Username) > 128 || len(body.Password) > 256 || len(body.GuestKey) > 128 { // B9
		writeError(w, 400, "input too long")
		return
	}
	if !auth.ValidateGuestKeyForRegistration(body.GuestKey) {
		writeError(w, 400, "invalid or already used guest key")
		return
	}
	if err := auth.RegisterCollaborator(body.Username, body.Password, body.GuestKey); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Mark guest key as collaborator type
	if guestKeyStore != nil {
		guestKeyStore.SetShareType(body.GuestKey, "collaborator")
		guestKeyStore.MarkAsCollaborator(body.GuestKey)
	}
	// Create JWT token for auto-login
	accessToken, _ := auth.CreateToken(body.Username, true)
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"access_token": accessToken,
		"role":         "collaborator",
		"username":     body.Username,
	})
}

func handleAdminSettingsJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedJS(w, r, "admin-settings.js")
}

func handleAdminNetworkJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedJS(w, r, "admin-network.js")
}

func handleAdminShareJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedJS(w, r, "admin-share.js")
}

func handleAdminLogsJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedJS(w, r, "admin-logs.js")
}