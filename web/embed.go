// Package web embeds the compiled frontend directly into the server binary
// via go:embed, so the whole demo ships as a single self-contained
// executable (plus its ClickHouse dependency) with no separate static
// asset deployment step.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static/*
var embedded embed.FS

// FS returns the embedded frontend rooted at web/static, so index.html is
// served at "/" rather than "/static/".
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
