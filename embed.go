package main

import (
	"embed"
	"io/fs"
)

//go:embed web
var webEmbedFS embed.FS

var webFS fs.FS

func init() {
	var err error
	webFS, err = fs.Sub(webEmbedFS, "web")
	if err != nil {
		panic(err)
	}
}
