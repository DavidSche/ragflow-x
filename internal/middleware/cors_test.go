package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestCORSAllowedOriginAndCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Security{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
	}
	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if v := w.Header().Get("Access-Control-Allow-Origin"); v != "https://app.example.com" {
		t.Fatalf("expected echoed origin, got %q", v)
	}
	if v := w.Header().Get("Access-Control-Allow-Credentials"); v != "true" {
		t.Fatalf("expected credentials true, got %q", v)
	}
}

func TestCORSDeniesUnknownOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Security{AllowedOrigins: []string{"https://allowed.example.com"}}
	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if v := w.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("expected no CORS for unknown origin, got %q", v)
	}
}

func TestCORSDisabledWhenNoOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(config.Security{})) // empty = same-origin only
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Method = http.MethodOptions
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", w.Code)
	}
	if v := w.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("expected no CORS headers, got %q", v)
	}
}
