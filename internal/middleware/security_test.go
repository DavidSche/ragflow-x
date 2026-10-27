package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Security{
		HSTSEnabled:           true,
		HSTSMaxAgeSec:         31536000,
		HSTSIncludeSubdomains: true,
		ContentSecurityPolicy: "default-src 'none'",
		FrameOptions:          "DENY",
	}
	r := gin.New()
	r.Use(SecurityHeaders(cfg))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	if v := w.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Fatalf("expected nosniff, got %q", v)
	}
	if v := w.Header().Get("X-Frame-Options"); v != "DENY" {
		t.Fatalf("expected X-Frame-Options DENY, got %q", v)
	}
	if v := w.Header().Get("Content-Security-Policy"); v != "default-src 'none'" {
		t.Fatalf("unexpected CSP: %q", v)
	}
	if v := w.Header().Get("Strict-Transport-Security"); v != "max-age=31536000; includeSubDomains" {
		t.Fatalf("unexpected HSTS: %q", v)
	}
}

func TestSecurityHeadersHSTSOptIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders(config.Security{})) // HSTS disabled by default
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if v := w.Header().Get("Strict-Transport-Security"); v != "" {
		t.Fatalf("HSTS must be opt-in, got %q", v)
	}
}
