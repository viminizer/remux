// Package web serves the phone UI compiled into the binary.
//
// go:embed is what makes remux a single file: there is no web/dist to ship
// alongside it and no static file path to configure.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist is populated by scripts/build.sh, which stages the Vite output here.
// Only .gitkeep is committed, so a fresh clone still compiles - go:embed needs
// the directory to exist, but not to hold a real build.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the built UI, falling back to index.html so the hash router
// still works on a deep link like /#/p/%14 after a hard reload.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil || !Built() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.Error(w, notBuiltPage, http.StatusServiceUnavailable)
		})
	}
	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// Unknown path: hand back the shell and let the client route.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		// The service worker must not be cached, or a stale one keeps
		// serving an old shell after an upgrade.
		if strings.HasSuffix(p, "sw.js") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Go's mime table does not know .webmanifest, and Chrome will not
		// offer "Add to Home screen" for one served as text/plain.
		if strings.HasSuffix(p, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}

const notBuiltPage = `<!doctype html><meta charset="utf-8"><title>remux</title>` +
	`<body style="font:14px system-ui;background:#1e1e2e;color:#cdd6f4;padding:40px">` +
	`<h1 style="font-size:18px">The UI is not in this binary</h1>` +
	`<p>Build it with <code>./scripts/build.sh</code>, which runs the Vite build ` +
	`and stages it into <code>internal/web/dist</code> before compiling.</p>`

// Built reports whether a real UI was embedded, so --local can say so plainly
// instead of serving a blank page.
func Built() bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}
