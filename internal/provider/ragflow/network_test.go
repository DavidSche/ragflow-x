package ragflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientRejectsCrossHostRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://untrusted.example/api/v1/health", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	client := NewHTTPClient(server.URL, "api-key", time.Second, 1)
	err := client.do(context.Background(), http.MethodGet, "/health", nil, "", nil)
	cause := errors.Unwrap(err)
	if err == nil || cause == nil || !strings.Contains(cause.Error(), "configured host") {
		t.Fatalf("expected cross-host redirect rejection, got %v", err)
	}
}
