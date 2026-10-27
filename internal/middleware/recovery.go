package middleware

import (
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// Recovery converts panics into a 500 response while logging the stack.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered", "err", r, "path", c.Request.URL.Path, "stack", string(debug.Stack()))
				response.Fail(c, 500, 500, "internal server error")
				c.Abort()
			}
		}()
		c.Next()
	}
}
