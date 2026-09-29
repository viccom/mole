package shellui

import (
	"embed"
	"io/fs"
)

//go:embed static/*
var rawAssets embed.FS

// Assets returns the shared Wails shell assets used by desktop and manager.
func Assets() fs.FS {
	sub, err := fs.Sub(rawAssets, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
