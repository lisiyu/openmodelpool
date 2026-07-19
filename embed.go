package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed admin.html admin-provider.html admin-models.html admin-browser-login.html admin-common.js admin-settings.js admin-network.js admin-share.js admin-logs.js setup.html login.html
var htmlFS embed.FS

// serveEmbeddedHTML serves an HTML file from the embedded filesystem.
// This eliminates all file path dependency — HTML files are baked into the binary.
func serveEmbeddedHTML(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(htmlFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
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
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Write(data)
}
