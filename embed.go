package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed admin.html setup.html login.html
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
