package ragflow

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatStreamIsNotCutByTheInteractiveTimeout is the regression test for the
// storage review of 2026-11-27: six console answers stopped at exactly 30.000 s
// because the SSE body was read through the client whose whole-request budget is
// `ragflow.timeout`. The engine was still producing; the client had given up.
//
// The shape below is the field shape: headers arrive early, the body keeps
// trickling well past the interactive budget. The stream must deliver every frame.
func TestChatStreamIsNotCutByTheInteractiveTimeout(t *testing.T) {
	frames := []string{
		"data:{\"code\":0,\"data\":{\"answer\":\"第一段\"}}\n\n",
		"data:{\"code\":0,\"data\":{\"answer\":\"第二段\"}}\n\n",
		"data:{\"code\":0,\"data\":{\"answer\":\"第三段\"}}\n\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Frames keep coming after the interactive budget (50 ms) has elapsed.
		for _, f := range frames {
			time.Sleep(120 * time.Millisecond)
			if _, err := io.WriteString(w, f); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	// A deliberately tiny interactive budget: the point is that it no longer
	// applies to the stream body at all.
	client := newHTTPClient(srv.URL, "key", 50*time.Millisecond, 2*time.Second, 4)

	var out bytes.Buffer
	if err := client.StreamChatCompletion(context.Background(), "chat-1", CompletionRequest{}, &out); err != nil {
		t.Fatalf("stream must outlive the interactive request budget, got %v", err)
	}
	for _, want := range []string{"第一段", "第二段", "第三段"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("frame %q missing from the delivered stream: %q", want, out.String())
		}
	}
}

// TestInteractiveCallsKeepTheConfiguredBudget pins the other half of the split:
// a call that reads one response and returns must still fail fast instead of
// hanging on a wedged engine.
func TestInteractiveCallsKeepTheConfiguredBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		writeEnvelope(t, w, []interface{}{})
	}))
	defer srv.Close()

	client := newHTTPClient(srv.URL, "key", 50*time.Millisecond, 2*time.Second, 4)

	started := time.Now()
	err := client.do(context.Background(), http.MethodGet, "/datasets", nil, "", nil)
	if err == nil {
		t.Fatal("an interactive call must still honour ragflow.timeout")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("interactive budget did not apply: call took %v", elapsed)
	}
}

// TestStreamStillBoundsTheWaitForHeaders: dropping the whole-request budget must
// not mean waiting forever. An engine that never sends headers is bounded by
// StreamHeaderDeadline, which is what turns a silent upstream into a 502 instead
// of a spinner the user cannot get out of.
func TestStreamStillBoundsTheWaitForHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()

	client := newHTTPClient(srv.URL, "key", 5*time.Second, 50*time.Millisecond, 4)

	started := time.Now()
	err := client.StreamChatCompletion(context.Background(), "chat-1", CompletionRequest{}, io.Discard)
	if err == nil {
		t.Fatal("a stream whose headers never arrive must fail")
	}
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("header wait was not bounded by the stream transport: call took %v", elapsed)
	}
}

// TestStreamClientDefaultsToTheStreamingOne guards the wiring: every SSE proxy
// must reach the client without a whole-request budget, not the interactive one.
func TestStreamClientDefaultsToTheStreamingOne(t *testing.T) {
	client := NewHTTPClient("http://127.0.0.1:1", "key", 30*time.Second, 4)
	if client.stream == nil {
		t.Fatal("NewHTTPClient must build a streaming client")
	}
	if client.stream.Timeout != 0 {
		t.Fatalf("streaming client must have no whole-request budget, got %v", client.stream.Timeout)
	}
	if client.streamClient() != client.stream {
		t.Fatal("streamClient must return the streaming client")
	}
	if client.http.Timeout != 30*time.Second {
		t.Fatalf("interactive client must keep the configured budget, got %v", client.http.Timeout)
	}
}
