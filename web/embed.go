// Package web embeds the production SPA build for the Go server.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the embedded Vite build directory.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
