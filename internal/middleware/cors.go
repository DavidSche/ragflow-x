package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// CORS applies configurable cross-origin headers. With no allowed origins the
// middleware is effectively disabled (same-origin only). When credentials are
// allowed, echoing a concrete origin is required (never "*").
func CORS(cfg config.Security) gin.HandlerFunc {
	return corsHandler(cfg)
}

func corsHandler(cfg config.Security) gin.HandlerFunc {
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[strings.ToLower(strings.TrimSpace(o))] = true
	}
	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}
	}
	headers := cfg.AllowedHeaders
	if len(headers) == 0 {
		headers = []string{"Content-Type", "Authorization", "X-Request-Id"}
	}
	maxAge := cfg.MaxAgeSec
	if maxAge <= 0 {
		maxAge = 600
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		var echoed string
		if origin != "" {
			switch {
			case allowed["*"]:
				echoed = "*"
			case allowed[strings.ToLower(origin)]:
				echoed = origin
			}
		}
		if echoed != "" {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", echoed)
			h.Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
			h.Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
			h.Set("Access-Control-Max-Age", strconv.Itoa(maxAge))
			h.Add("Vary", "Origin")
			if cfg.AllowCredentials && echoed != "*" {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
