//go:build !webfs_embed

package webfs

import (
	"embed"
	"io/fs"
)

// Placeholder build (no webfs_embed tag): serves a small placeholder page so
// `go build ./...` / `go vet` work without a prior frontend build.
//
//go:embed all:placeholder
var placeholderFS embed.FS

// assets returns the placeholder FS rooted at its index.html.
func assets() fs.FS {
	f, err := fs.Sub(placeholderFS, "placeholder")
	if err != nil {
		// Cannot happen: placeholder/ is embedded above.
		panic(err)
	}
	return f
}
