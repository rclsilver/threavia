// Package web serves the Core web client.
//
// The client is a Vite + React application built into static files and embedded
// here, so a release is one binary with no asset directory to deploy beside it.
// In development it is served by Vite instead, which proxies to Core: editing a
// component reloads the page without rebuilding or restarting this binary.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// assets holds the built client.
//
// The `all:` prefix is what lets this compile before anything is built: the
// directory ships with only a .gitkeep, which the default pattern would skip,
// leaving nothing to embed and a build error. Handler then reports the honest
// state rather than serving a blank page.
//
//go:embed all:ui/dist
var assets embed.FS

// Built reports whether a client was built into this binary.
func Built() bool {
	_, err := fs.Stat(assets, "ui/dist/index.html")
	return err == nil
}

// Handler serves the web client at the root.
//
// Unknown paths fall back to the entry document, because the client routes on
// the browser history: a reload on /sessions/<id> is a request this server has
// never heard of and must still answer with the application.
func Handler() (http.Handler, error) {
	if !Built() {
		return http.HandlerFunc(notBuilt), nil
	}

	root, err := fs.Sub(assets, "ui/dist")
	if err != nil {
		return nil, err
	}
	files := http.FileServerFS(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(root, name); err == nil {
			// A built asset carries a content hash in its name, so it can be
			// cached as long as the browser likes: a new build is a new name.
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}

		// A client route. The document itself is never cached, or a deploy would
		// keep serving the previous application from the browser.
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	}), nil
}

// notBuilt answers when the binary carries no client.
//
// It says so plainly rather than returning a blank page or a 404: a developer
// running `go build` without building the client should learn that from the
// page, not from an empty tab.
func notBuilt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(
		"The web client is not built into this binary.\n\n" +
			"Build it:        make web\n" +
			"Or develop it:   make dev-web   (hot reload on http://localhost:5173)\n\n" +
			"The API is unaffected and is served normally under /api/v1.\n"))
}
