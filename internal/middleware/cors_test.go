package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestCORSPreflightExposesContractHeaders pins doc/301 §5.10 GW-1c. Two failures this
// catches that a coarse "CORS is on" test does not:
//
//  1. a browser can send every header it is allowed to send but still be unable to
//     *read* the trace id and quota headers, because `Expose-Headers` was never set
//     anywhere in the repo; and
//  2. `security.allowed_headers` is tenant-overridable, so a stored value that predates
//     `Idempotency-Key` would reject the preflight the gateway itself requires.
func TestCORSPreflightExposesContractHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Deliberately a stale tenant configuration: no Idempotency-Key anywhere.
	cfg := config.Security{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "X-Request-Id"},
	}
	r := gin.New()
	r.Use(CORS(cfg))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,idempotency-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", w.Code)
	}
	allowHeaders := w.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allowHeaders), "idempotency-key") {
		t.Fatalf("Allow-Headers %q must contain Idempotency-Key even when the tenant setting omits it", allowHeaders)
	}
	// The tenant's own entries survive the union.
	for _, kept := range cfg.AllowedHeaders {
		if !strings.Contains(allowHeaders, kept) {
			t.Fatalf("Allow-Headers %q dropped configured header %q", allowHeaders, kept)
		}
	}
	exposed := w.Header().Get("Access-Control-Expose-Headers")
	for _, want := range []string{"X-Trace-Id", "X-Quota-Remaining", "X-Quota-Requests-Remaining", "X-RGX-Policy", "X-RGX-Degraded"} {
		if !strings.Contains(exposed, want) {
			t.Fatalf("Expose-Headers %q is missing %q", exposed, want)
		}
	}
}

// TestCORSDoesNotDuplicateContractHeaders guards the union against growing a duplicate
// on every request: `append` into a shared slice would do exactly that once the config
// value already contains the header, and the resulting header line is harmless but the
// bug it hides is not (a slice aliasing the caller's config).
func TestCORSDoesNotDuplicateContractHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Security{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedHeaders: []string{"Content-Type", "idempotency-key"},
	}
	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	allowHeaders := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	if strings.Count(allowHeaders, "idempotency-key") != 1 {
		t.Fatalf("Allow-Headers %q repeated Idempotency-Key", allowHeaders)
	}
	if len(cfg.AllowedHeaders) != 2 {
		t.Fatalf("CORS mutated the caller's config slice: %v", cfg.AllowedHeaders)
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
