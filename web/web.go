// Package web serves the Core web client.
//
// The client is a single self-contained page: no build step, no bundler, no
// framework. It talks to Core over HTTP/JSON and SSE only, holds no state of its
// own, and closing it never stops agent execution.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed ui
var assets embed.FS

// Handler serves the web client at the root.
func Handler() (http.Handler, error) {
	root, err := fs.Sub(assets, "ui")
	if err != nil {
		return nil, err
	}
	return http.FileServerFS(root), nil
}
