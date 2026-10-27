package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func DynamicSecurityHeaders(getConfig func() config.Security) gin.HandlerFunc {
	return func(c *gin.Context) {
		securityHeadersHandler(getConfig())(c)
	}
}

func DynamicCORS(getConfig func() config.Security) gin.HandlerFunc {
	return func(c *gin.Context) {
		corsHandler(getConfig())(c)
	}
}

func DynamicCookieCSRF(getOrigins func() []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookieCSRFHandler(getOrigins())(c)
	}
}

func DynamicBodyLimit(getMaxBytes func() int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		bodyLimitHandler(getMaxBytes())(c)
	}
}

func DynamicMetricsAuth(getConfig func() (string, []string)) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, allowCIDRs := getConfig()
		metricsAuthHandler(token, allowCIDRs)(c)
	}
}
