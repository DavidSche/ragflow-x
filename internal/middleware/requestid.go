package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ragflow-x/ragflow-x/internal/pkg/requestid"
)

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
		c.Next()
	}
}
