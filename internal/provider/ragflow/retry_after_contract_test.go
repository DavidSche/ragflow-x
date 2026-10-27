package ragflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RetryExhaustionPreservesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":0,"message":"throttled","data":null}`))
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", time.Second, 1,
		RetryMiddleware(0, time.Millisecond))
	var out map[string]interface{}
	err := client.do(context.Background(), http.MethodGet, "/test", nil, "", &out)
	if err == nil {
		t.Fatal("expected retry exhaustion error")
	}
	var providerErr *Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if providerErr.HTTPStatus != http.StatusTooManyRequests || providerErr.Type != ErrorTypeTransport {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
	if providerErr.RetryAfterMs != 3000 {
		t.Fatalf("RetryAfterMs = %d, want 3000", providerErr.RetryAfterMs)
	}
}
