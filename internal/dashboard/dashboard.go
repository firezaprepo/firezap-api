// Package dashboard serves the web dashboard: plain HTML, CSS and JavaScript embedded in the
// binary (no build step, no external requests). The browser calls the same REST API with the
// admin key the user types in; the server keeps no dashboard session.
package dashboard

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web
var files embed.FS

// securityHeaders lock the dashboard down: only its own files, no inline scripts, no framing.
// The QR code arrives as a data: image, so img-src allows data:.
func securityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; object-src 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-cache")
}

// Handler serves the dashboard under /dashboard/. defaultLanguage is "en" or "pt-BR".
func Handler(defaultLanguage, version string) http.Handler {
	web, err := fs.Sub(files, "web")
	if err != nil {
		panic(err) // the files are embedded at build time
	}
	static := http.StripPrefix("/dashboard/", http.FileServer(http.FS(web)))
	cfg, _ := json.Marshal(map[string]string{"defaultLanguage": defaultLanguage, "version": version})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w.Header())
		switch path := strings.TrimPrefix(r.URL.Path, "/dashboard/"); {
		case path == "config.json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(cfg)
		case strings.Contains(path, ".."):
			http.NotFound(w, r)
		default:
			static.ServeHTTP(w, r)
		}
	})
}
