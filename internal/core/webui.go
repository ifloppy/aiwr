package core

import (
	"embed"
	"net/http"
)

// The UI is intentionally embedded in the native server. This keeps the
// default deployment single-process and avoids a second frontend build or a
// CDN dependency for a local-first tool.
//
//go:embed webui/index.html webui/app.js webui/styles.css
var webUIFiles embed.FS

func knownWebUIPath(path string) bool {
	switch path {
	case "/", "/app.js", "/styles.css":
		return true
	default:
		return false
	}
}

func serveWebUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := ""
	contentType := ""
	switch r.URL.Path {
	case "/":
		name, contentType = "webui/index.html", "text/html; charset=utf-8"
	case "/app.js":
		name, contentType = "webui/app.js", "text/javascript; charset=utf-8"
	case "/styles.css":
		name, contentType = "webui/styles.css", "text/css; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}
	data, err := webUIFiles.ReadFile(name)
	if err != nil {
		http.Error(w, "web UI asset unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	_, _ = w.Write(data)
}
