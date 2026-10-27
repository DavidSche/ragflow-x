package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// CookieCSRF rejects ambient browser requests that were initiated from a
// different site. Bearer-token API clients are unaffected because they do not
// rely on ambient cookies.
func CookieCSRF(allowedOrigins []string) gin.HandlerFunc {
	return cookieCSRFHandler(allowedOrigins)
}

func cookieCSRFHandler(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if normalized := normalizeOrigin(origin); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		if value, err := c.Cookie("rgx_access"); err != nil || value == "" {
			if value, err := c.Cookie("rgx_refresh"); err != nil || value == "" {
				c.Next()
				return
			}
		}
		if c.GetHeader("Authorization") != "" {
			c.Next()
			return
		}

		if site := c.GetHeader("Sec-Fetch-Site"); site != "" && strings.EqualFold(site, "cross-site") {
			response.Fail(c, http.StatusForbidden, 40300, "invalid request origin")
			c.Abort()
			return
		}

		// Browsers set this header themselves and do not permit callers to
		// override it. Proxies commonly rewrite Host while preserving Origin,
		// so an explicit browser-declared same-origin request is safe even when
		// the rewritten backend Host no longer matches.
		if site := c.GetHeader("Sec-Fetch-Site"); site != "" && strings.EqualFold(site, "same-origin") {
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		if _, ok := allowed[normalizeOrigin(origin)]; ok || sameOrigin(origin, c.Request.Host) {
			c.Next()
			return
		}
		response.Fail(c, http.StatusForbidden, 40300, "invalid request origin")
		c.Abort()
	}
}

func normalizeOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func sameOrigin(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}
