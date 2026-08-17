package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed admin.html admin-provider.html admin-models.html admin-browser-login.html admin-free-pool.html admin-common.js admin-settings.js admin-network.js admin-share.js admin-logs.js admin-update.js admin-ledger.js federation-health.html setup.html login.html
var htmlFS embed.FS

// serveEmbeddedHTML serves an HTML file from the embedded filesystem.
// This eliminates all file path dependency — HTML files are baked into the binary.
func serveEmbeddedHTML(w http.ResponseWriter, r *http.Request, name string, framable bool) {
	data, err := fs.ReadFile(htmlFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if framable {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		w.Header().Set("X-Frame-Options", "DENY")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(data)
}

// serveEmbeddedJS serves a JavaScript file from the embedded filesystem.
func serveEmbeddedJS(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(htmlFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(data)
}
