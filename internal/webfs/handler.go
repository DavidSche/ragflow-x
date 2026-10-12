package webfs

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// assets returns the embedded frontend filesystem rooted at its index.html.
// Each build-tag variant (dist_placeholder.go / dist_embedded.go) supplies it.

// Handler is the Gin no-route handler serving the embedded frontend with SPA
// fallback. Unknown non-/api paths fall back to index.html so deep links like
// /datasets/123 survive a hard refresh; assets are content-hashed by Vite and
// marked immutable, while index.html is never cached.
func Handler() gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(assets()))
	return func(c *gin.Context) {
		// API and operational endpoints must never fall back to index.html:
		// a missing endpoint is a JSON 404, not the UI shell.
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || p == "/metrics" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		name := strings.TrimPrefix(p, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(assets(), path.Clean(name)); err == nil {
			if name != "index.html" {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Header("Content-Type", "text/html; charset=utf-8")
		data, err := fs.ReadFile(assets(), "index.html")
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "frontend assets unavailable"})
			return
		}
		_, _ = c.Writer.Write(data)
	}
}
