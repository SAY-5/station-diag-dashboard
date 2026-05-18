// Package web embeds the static dashboard frontend. The embed paths use
// forward slashes, which embed.FS requires on every OS including Windows.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static/index.html static/app.js static/style.css
var files embed.FS

// FS returns the embedded frontend rooted so that "/" serves index.html.
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic("web: embed sub: " + err.Error())
	}
	return sub
}
