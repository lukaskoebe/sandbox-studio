// Package webui serves the embedded single-page app built from web/.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// static/app is produced by `pnpm build` in web/; static/fallback.html is committed.
//
//go:embed all:static
var static embed.FS

// Handler serves the SPA: real files when they exist, index.html for client routes.
func Handler() http.Handler {
	app, err := fs.Sub(static, "static/app")
	if err != nil || !exists(app, "index.html") {
		return fallback()
	}
	return spa(app)
}

func spa(app fs.FS) http.Handler {
	files := http.FileServerFS(app)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && exists(app, name) {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, app, "index.html")
	})
}

func fallback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "static/fallback.html")
	})
}

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
