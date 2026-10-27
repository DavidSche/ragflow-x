package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// RequestLogger emits a structured access log for each request using the
// process-wide structured logger. It must be registered after RequestID so
// the request id can be attached.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		rid, _ := c.Get("request_id")
		logger.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"request_id", rid,
			"bytes", c.Writer.Size(),
		)
	}
}
