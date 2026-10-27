package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// SecurityHeaders applies hardened, denial-by-default response headers on every
// response, plus HSTS when explicitly enabled (only appropriate behind TLS).
func SecurityHeaders(cfg config.Security) gin.HandlerFunc {
	return securityHeadersHandler(cfg)
}

func securityHeadersHandler(cfg config.Security) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		frame := cfg.FrameOptions
		if frame == "" {
			frame = "DENY"
		}
		h.Set("X-Frame-Options", frame)
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Referrer-Policy", "no-referrer")
		if csp := cfg.ContentSecurityPolicy; csp != "" {
			h.Set("Content-Security-Policy", csp)
		}
		if cfg.HSTSEnabled {
			value := fmt.Sprintf("max-age=%d", cfg.HSTSMaxAgeSec)
			if cfg.HSTSIncludeSubdomains {
				value += "; includeSubDomains"
			}
			h.Set("Strict-Transport-Security", value)
		}
		c.Next()
	}
}
