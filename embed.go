package main

import (
	"embed"
	"io/fs"
)

//go:embed all:client/dist
var frontendFS embed.FS

// Frontend returns the embedded React build rooted at client/dist, or nil if it
// hasn't been built yet (the directory may still embed as an empty FS).
func Frontend() fs.FS {
	sub, err := fs.Sub(frontendFS, "client/dist")
	if err != nil {
		return nil
	}
	// If empty (no index.html), report nil so the server can show a clear error.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
