package webfs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(Handler())
	return r
}

func get(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestSPAFallbackAnswers200 pins the status code of the SPA shell. Gin marks the
// NoRoute path as 404 before the handler runs; the fallback renders index.html,
// so it has to claim 200 or a working page is reported as missing (a first-run
// release binary answered "404 page not found" for GET / before this).
func TestSPAFallbackAnswers200(t *testing.T) {
	r := newTestRouter()
	for _, path := range []string{"/", "/setup", "/datasets/123", "/dashboard?tab=1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: SPA shell must answer 200, got %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: expected html, got %q", path, ct)
		}
		if !strings.Contains(rec.Body.String(), "RAGFlow-X") {
			t.Fatalf("%s: body is not the bundled index.html: %q", path, rec.Body.String())
		}
	}
}

// TestSPAFallbackKeepsAPINotFoundJSON keeps the API surface honest: a missing
// endpoint is a JSON 404, never the UI shell.
func TestSPAFallbackKeepsAPINotFoundJSON(t *testing.T) {
	r := newTestRouter()
	for _, path := range []string{"/api/v1/does-not-exist", "/metrics"} {
		rec := get(t, r, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "<html") || !strings.Contains(body, "not found") {
			t.Fatalf("%s: expected JSON 404 body, got %q", path, body)
		}
	}
}

// TestIndexIsNotCached guards the cache split: hashed assets are immutable,
// index.html must never be cached (otherwise a deploy leaves stale asset URLs).
func TestIndexIsNotCached(t *testing.T) {
	r := newTestRouter()
	rec := get(t, r, "/")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index.html must not be cached, got Cache-Control %q", cc)
	}
}
