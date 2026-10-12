//go:build webfs_embed

package webfs

import (
	"embed"
	"io/fs"
	"os"
)

// Release build (tag webfs_embed): embeds the Vite production build. The
// release pipeline copies web/dist assets into this directory (internal/webfs/dist)
// before `go build -tags=webfs_embed`, because go:embed cannot reach outside
// the package directory.
//
//go:embed all:dist
var distFS embed.FS

// assets returns the embedded frontend FS rooted at its index.html.
func assets() fs.FS {
	f, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Cannot happen: dist/ is embedded above. Kept as a guard so a
		// malformed build (missing dist/) fails here, not deep in a request.
		if _, statErr := os.Stat("dist"); statErr != nil {
			panic("webfs: webfs_embed tag set but internal/webfs/dist is missing (run the web build first)")
		}
		panic(err)
	}
	return f
}
