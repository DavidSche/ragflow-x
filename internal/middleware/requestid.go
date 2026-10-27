package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ragflow-x/ragflow-x/internal/pkg/requestid"
)

// TraceIDHeader is the canonical trace alias written on every response.
// A single request-id value is exposed under both the configured request-id
// header and X-Trace-Id so gateway consumers can correlate logs and provider
// calls with one token (doc/224 T5.2 standard response headers).
const TraceIDHeader = "X-Trace-Id"

// RequestID injects or generates a request id and stores it in the context
// and response header. The header key is configurable.
func RequestID(header string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(header)
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Request = c.Request.WithContext(requestid.NewContext(c.Request.Context(), rid))
		c.Set("request_id", rid)
		c.Writer.Header().Set(header, rid)
		c.Writer.Header().Set(TraceIDHeader, rid)
		c.Next()
	}
}
