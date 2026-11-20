package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// ContractHeaders are the request headers the API contract *requires* browsers to be
// able to send, regardless of tenant configuration (doc/301 §5.10 GW-1c).
//
// `Idempotency-Key` is here because three gateway entry points read it
// (`handler/gateway.go`), and `security.allowed_headers` is a database-overridable
// `string_list` setting: a tenant whose stored value predates this release would keep
// getting preflights rejected, and the browser would report it as a CORS failure
// rather than a missing key. The union is written in code so the contract cannot be
// configured away.
var ContractHeaders = []string{"Idempotency-Key"}

// ExposedHeaders are the response headers a browser page is allowed to *read*.
//
// `Access-Control-Expose-Headers` is a contract, not a tenant knob: without it the
// console can make a gateway call and then be unable to see `X-Trace-Id` or the
// remaining quota — the values are on the wire and invisible at the same time. The
// list is code-resident for the same reason as ContractHeaders.
var ExposedHeaders = []string{
	"X-Trace-Id",
	"X-Quota-Remaining",
	"X-Quota-Requests-Remaining",
	"X-RGX-Policy",
	"X-RGX-Degraded",
}

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
	// Union, not replacement: the configured list stays authoritative for everything a
	// tenant added, and the contract headers are appended when missing. Appending keeps
	// the configured order stable, so a debugging operator sees the same string they
	// typed plus the required tail.
	headers := append([]string{}, cfg.AllowedHeaders...)
	if len(headers) == 0 {
		headers = []string{"Content-Type", "Authorization", "X-Request-Id"}
	}
	for _, required := range ContractHeaders {
		if !containsHeader(headers, required) {
			headers = append(headers, required)
		}
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
			h.Set("Access-Control-Expose-Headers", strings.Join(ExposedHeaders, ", "))
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

// containsHeader compares case-insensitively: HTTP header names are case-insensitive
// and a tenant writing `idempotency-key` is making the same request.
func containsHeader(headers []string, want string) bool {
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), want) {
			return true
		}
	}
	return false
}
