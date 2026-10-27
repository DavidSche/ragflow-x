package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

const ContextServiceReport = "service_report"

type RuntimeReportTokenProvider func() string

func RuntimeReportAuth(provider RuntimeReportTokenProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := provider()
		if token == "" {
			response.Fail(c, http.StatusServiceUnavailable, 503, "runtime report service token is not configured")
			c.Abort()
			return
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			response.Fail(c, http.StatusUnauthorized, 401, "missing runtime report token")
			c.Abort()
			return
		}
		submitted := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
			response.Fail(c, http.StatusUnauthorized, 401, "invalid runtime report token")
			c.Abort()
			return
		}
		c.Set(ContextServiceReport, true)
		c.Next()
	}
}
