// Package web embeds the built React UI and serves it as static files.
//
// The UI build is produced by `npm run build` in ui/, which writes to
// internal/web/dist (configured via vite's outDir). If the UI has not been
// built, dist holds only a placeholder and [Handler] reports unavailable.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler serving the embedded UI, and ok=false if the
// UI was not built into this binary (run `npm run build` in ui/ first).
//
// It serves real files as-is and falls back to index.html for any other path,
// so client-side routes like /register?invite=… resolve to the single-page
// app instead of 404ing.
func Handler() (handler http.Handler, ok bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, false
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, false // UI not built
	}
	files := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if f, err := sub.Open(name); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: unknown path -> index.html.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}), true
}
