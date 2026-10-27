package ragflow

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ScenarioID: SC-SSE-001
func TestP0_SSE_001_StreamChatCompletionPreservesUTF8BoundaryAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstFlushed := make(chan struct{})
	requestClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(requestClosed)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		if _, err := io.WriteString(w, "data: {\"content\":\""); err != nil {
			return
		}
		if _, err := w.Write([]byte("中")[:1]); err != nil {
			return
		}
		flusher.Flush()
		close(firstFlushed)

		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()

	client := NewHTTPClientWithMiddleware(server.URL, "test-key", 5*time.Second, 1)
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- client.StreamChatCompletion(ctx, "chat-1", CompletionRequest{Messages: nil}, &output)
	}()

	select {
	case <-firstFlushed:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not flush the first UTF-8 boundary chunk")
	}
	deadline := time.Now().Add(2 * time.Second)
	for output.Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("client did not receive the flushed UTF-8 boundary chunk")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation to terminate the stream with an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not terminate after context cancellation")
	}
	select {
	case <-requestClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("provider request was not closed after cancellation")
	}

	if got := output.String(); bytes.Contains([]byte(got), []byte{0xEF, 0xBF, 0xBD}) {
		t.Fatalf("stream must not corrupt a partial UTF-8 boundary: %q", got)
	}
	if want := "data: {\"content\":\"\xE4"; !bytes.HasPrefix(output.Bytes(), []byte(want)) {
		t.Fatalf("unexpected stream prefix: %q", output.String())
	}
}
