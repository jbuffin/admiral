// Package web embeds the built UI.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// UI is the built UI, rooted at web/dist.
func UI() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
