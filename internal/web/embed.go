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

//go:embed all:dist
var dist embed.FS

// Handler serves the built UI, falling back to index.html so the hash router
// still works on a deep link like /#/p/%14 after a hard reload.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "ui not built", http.StatusInternalServerError)
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
		files.ServeHTTP(w, r)
	})
}

// Built reports whether a real UI was embedded, so --local can say so plainly
// instead of serving a blank page.
func Built() bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}
