package main

import "net/http"

// ============================================================
// Static pages
// ============================================================

func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "admin.html", false)
}

func handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if auth.Initialized() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "setup.html", false)
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "login.html", false)
}

func handleProviderPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "admin-provider.html", true)
}

func handleModelsPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "admin-models.html", true)
}

func handleBrowserLoginPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "admin-browser-login.html", true)
}

func handleFreePoolPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "admin-free-pool.html", true)
}

func handleFederationHealthPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	serveEmbeddedHTML(w, r, "federation-health.html", true)
}

func handleAdminCommonJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedJS(w, r, "admin-common.js")
}

// ============================================================
// Utility
// ============================================================

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}