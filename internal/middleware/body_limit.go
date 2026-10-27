package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// BodyLimit bounds the accepted request body. This protects handlers that
// read complete JSON bodies or uploads from memory exhaustion.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return bodyLimitHandler(maxBytes)
}

func bodyLimitHandler(maxBytes int64) gin.HandlerFunc {
	const defaultMaxBytes = 25 << 20
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
