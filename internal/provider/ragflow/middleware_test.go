package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// writeTestEnvelope writes a RAGFlow-style response for tests.
func writeTestEnvelope(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	env := map[string]interface{}{"code": 0, "message": "", "data": data}
	_ = json.NewEncoder(w).Encode(env)
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RetryMiddlewareOnlyIdempotent(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(500)
			return
		}
		writeTestEnvelope(w, map[string]interface{}{"ok": true})
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", 5*time.Second, 2,
		RetryMiddleware(3, 50*time.Millisecond))

	// GET should retry
	callCount = 0
	var out map[string]interface{}
	if err := client.do(context.Background(), http.MethodGet, "/test", nil, "", &out); err != nil {
		t.Fatalf("GET retry: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("GET should retry 3 times, got %d", callCount)
	}

	// POST should NOT retry
	callCount = 0
	if err := client.do(context.Background(), http.MethodPost, "/test", nil, "", &out); err == nil {
		t.Fatal("POST should not retry on 5xx")
	}
	if callCount != 1 {
		t.Fatalf("POST should not retry, got %d calls", callCount)
	}
}

func TestRetryMiddleware_ExhaustedReturnsStructuredError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", 5*time.Second, 2,
		RetryMiddleware(2, 10*time.Millisecond))

	var out map[string]interface{}
	err := client.do(context.Background(), http.MethodGet, "/test", nil, "", &out)
	if err == nil {
		t.Fatal("expected error")
	}
	// Retry exhaustion: errors.As hits *Error directly (no double-wrap)
	var ragErr *Error
	if !errors.As(err, &ragErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if ragErr.HTTPStatus != 500 {
		t.Fatalf("HTTPStatus = %d, want 500", ragErr.HTTPStatus)
	}
	if ragErr.Type != ErrorTypeTransport {
		t.Fatalf("Type = %d, want ErrorTypeTransport", ragErr.Type)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RetryMiddlewareStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	secondCall := false
	transport := RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			cancel()
			return nil, errors.New("connection reset")
		}
		secondCall = true
		return nil, errors.New("provider must not be called after cancellation")
	})
	decoratedTransport := RetryMiddleware(2, time.Millisecond)(transport)
	client := &HTTPClient{
		baseURL: "http://ragflow.test",
		apiKey:  "key",
		http:    &http.Client{Transport: decoratedTransport},
	}

	var out map[string]interface{}
	err := client.doAndParse(ctx, http.MethodGet, "/test", nil, "", &out)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if secondCall {
		t.Fatal("retry middleware attempted provider after cancellation")
	}
	if attempts != 1 {
		t.Fatalf("attempt count = %d, want 1", attempts)
	}
	var ragErr *Error
	if !errors.As(err, &ragErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if ragErr.Type == ErrorTypeTimeout {
		t.Fatalf("cancellation must not be classified as timeout: %+v", ragErr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled cause, got %v", err)
	}
}

func TestRetryMiddleware_429RetryAfter(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		writeTestEnvelope(w, map[string]interface{}{"ok": true})
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", 5*time.Second, 2,
		RetryMiddleware(3, 10*time.Millisecond))

	var out map[string]interface{}
	start := time.Now()
	if err := client.do(context.Background(), http.MethodGet, "/test", nil, "", &out); err != nil {
		t.Fatalf("429 retry: %v", err)
	}
	elapsed := time.Since(start)
	// Should wait at least Retry-After's 1 second
	if elapsed < 900*time.Millisecond {
		t.Fatalf("429 retry should respect Retry-After, elapsed %v", elapsed)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_HTTPClientClassifiesDeadlineAsTimeout(t *testing.T) {
	requestStarted := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", time.Second, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	var out map[string]interface{}
	err := client.do(ctx, http.MethodGet, "/test", nil, "", &out)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	var ragErr *Error
	if !errors.As(err, &ragErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if ragErr.Type != ErrorTypeTimeout {
		t.Fatalf("Type = %d, want ErrorTypeTimeout", ragErr.Type)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded cause, got %v", err)
	}
}

func TestStreamPassthrough_WithMiddleware(t *testing.T) {
	// RAGFlow v0.27.x style SSE with data: true terminator
	sse := "data: {\"code\":0,\"data\":{\"content\":\"hello\"}}\n\ndata: true\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", 5*time.Second, 2,
		RetryMiddleware(3, 50*time.Millisecond))

	var buf bytes.Buffer
	err := client.StreamChatCompletion(context.Background(), "c1",
		CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}}, &buf)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") || !strings.Contains(buf.String(), "data: true") {
		t.Fatalf("stream output: %q", buf.String())
	}
}

func TestLoggingMiddleware_RedactsSensitiveEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", 5*time.Second, 2,
		LoggingMiddleware("/providers/"))

	// Request to /providers/xxx/connection should redact body
	body := bytes.NewReader([]byte(`{"api_key":"secret123","base_url":"http://example.com"}`))
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/v1/providers/openai/connection", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "key")

	resp, err := client.http.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	// Smoke round-trip: redaction correctness is asserted in TestLoggingMiddleware_RedactRequestBody.
}

func TestLoggingMiddleware_RedactRequestBody(t *testing.T) {
	rt := &loggingRT{sensitivePrefixes: []string{"/providers/"}}

	cases := []struct {
		name string
		path string
	}{
		{"api-v1-prefixed", "/api/v1/providers/openai/connection"},
		{"plain-inner-path", "/providers/openai/connection"},
		{"reverse-proxy-mount", "/ragflow/api/v1/providers/openai/connection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://engine.example"+tc.path,
				strings.NewReader(`{"api_key":"secret123","base_url":"http://example.com"}`))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			got := rt.logRequestBody(req)
			if got != "[redacted]" {
				t.Fatalf("logRequestBody(%q) = %q, want %q", tc.path, got, "[redacted]")
			}
			if strings.Contains(got, "secret123") {
				t.Fatalf("logRequestBody(%q) leaked api_key: %q", tc.path, got)
			}
		})
	}

	// Non-sensitive paths are not redacted and a GET without body renders empty.
	t.Run("non-sensitive-not-redacted", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://engine.example/api/v1/datasets", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if got := rt.logRequestBody(req); got != "" {
			t.Fatalf("GET without body should render empty, got %q", got)
		}
	})

	// End-to-end through the default chain (NewHTTPClient mounts
	// LoggingMiddleware("/providers/")): the request completes and the exact
	// redacted render above is what feeds logger.Info.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/v1/providers/openai/connection",
		strings.NewReader(`{"api_key":"secret123"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.http.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
}

func TestNormalizeMetricPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "compact-uuid-segments",
			path: "/api/v1/datasets/" + strings.Repeat("a", 32) + "/documents/" + strings.Repeat("b", 32) + "/chunks/" + strings.Repeat("c", 32),
			want: "/datasets/{id}/documents/{id}/chunks/{id}",
		},
		{
			name: "full-rfc4122-uuid",
			path: "/api/v1/chats/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: "/chats/{id}",
		},
		{
			name: "reverse-proxy-mount",
			path: "/ragflow/api/v1/datasets/" + strings.Repeat("a", 32),
			want: "/datasets/{id}",
		},
		{
			name: "plain-collection-no-id",
			path: "/api/v1/datasets",
			want: "/datasets",
		},
		{
			name: "non-hex-name-kept",
			path: "/api/v1/datasets/mydataset-name",
			want: "/datasets/mydataset-name",
		},
		{
			name: "long-hex-name-not-id",
			path: "/api/v1/datasets/" + strings.Repeat("a", 40),
			want: "/datasets/" + strings.Repeat("a", 40),
		},
		{
			name: "empty-path",
			path: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeMetricPath(tc.path); got != tc.want {
				t.Fatalf("normalizeMetricPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// BenchmarkMiddlewareChain measures the full production chain
// (Retry → Logging → Metrics → http.Client) per request.
func BenchmarkMiddlewareChain(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestEnvelope(w, map[string]interface{}{"ok": true})
	}))
	defer srv.Close()

	client := NewHTTPClientWithMiddleware(srv.URL, "key", 5*time.Second, 4,
		MetricsMiddleware(),
		LoggingMiddleware("/providers/"),
		RetryMiddleware(1, time.Millisecond),
	)

	var out map[string]interface{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := client.do(context.Background(), http.MethodGet, "/datasets", nil, "", &out); err != nil {
			b.Fatalf("do: %v", err)
		}
	}
}

// TestStreamSingleLogThroughDefaultConstructor locks the Phase 4 contract:
// streaming through the default NewHTTPClient (which mounts the default
// LoggingMiddleware exactly once) must emit exactly one request/response log
// pair, and must not emit inline Debug logs from the streaming methods.
//
// The logger is never initialized in this package's tests, so logger.Info
// writes JSON to os.Stdout per call. Swapping os.Stdout here is race-free
// because no test in this package registers t.Parallel().
func TestStreamSingleLogThroughDefaultConstructor(t *testing.T) {
	sse := "data: {\"code\":0,\"data\":{\"content\":\"hello\"}}\n\ndata: true\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
		_ = w.Close()
		_ = r.Close()
	}()

	var buf bytes.Buffer
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	if err := client.StreamChatCompletion(context.Background(), "c1",
		CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}}, &buf); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") || !strings.Contains(buf.String(), "data: true") {
		t.Fatalf("stream output: %q", buf.String())
	}

	_ = w.Close()
	captured := make([]byte, 1<<16)
	n, _ := r.Read(captured)
	out := string(captured[:n])

	if got := strings.Count(out, `"msg":"ragflow request"`); got != 1 {
		t.Fatalf("streaming should log exactly one middleware request line, got %d\n%s", got, out)
	}
	if got := strings.Count(out, `"msg":"ragflow response"`); got != 1 {
		t.Fatalf("streaming should log exactly one middleware response line, got %d\n%s", got, out)
	}
	if strings.Contains(out, `"level":"DEBUG"`) {
		t.Fatalf("streaming should not emit inline Debug logs\n%s", out)
	}
}
