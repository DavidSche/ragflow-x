package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// sseTestServer serves `frames` SSE frames, one per `gap`, through `write`. The
// server's WriteTimeout is set below one frame-gap past the start so a connection
// that never extends its deadline is severed mid-stream.
func sseTestServer(t *testing.T, writeTimeout, gap time.Duration, frames int, write func(gin.ResponseWriter, int) error) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/sse", func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < frames; i++ {
			time.Sleep(gap)
			if err := write(c.Writer, i); err != nil {
				return
			}
			c.Writer.Flush()
		}
	})
	srv := httptest.NewUnstartedServer(engine)
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestDeferredStreamWriterOutlivesServerWriteTimeout is the regression test for
// the second ceiling behind the 2026-11-27 truncations: `write_timeout` is an
// absolute deadline measured from the start of the request, so every chat answer
// longer than it (60 s by default) would have been cut even after the provider
// budget was fixed. Each frame now pushes the deadline forward.
func TestDeferredStreamWriterOutlivesServerWriteTimeout(t *testing.T) {
	srv := sseTestServer(t, 150*time.Millisecond, 100*time.Millisecond, 4,
		func(w gin.ResponseWriter, i int) error {
			_, err := io.WriteString(newDeferredStreamWriter(w), fmt.Sprintf("data:%d\n\n", i))
			return err
		})

	resp, err := http.Get(srv.URL + "/sse")
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading a stream that must outlive write_timeout failed: %v", err)
	}
	// Four frames 100 ms apart take ~400 ms, well past the 150 ms deadline.
	for i := 0; i < 4; i++ {
		if !strings.Contains(string(body), fmt.Sprintf("data:%d", i)) {
			t.Fatalf("frame %d missing: the write deadline was not extended, body=%q", i, body)
		}
	}
}

// TestServerWriteTimeoutCutsUnprotectedStreams is the control: without the
// extension the same handler is cut, so the test above proves the fix rather
// than a generous server.
func TestServerWriteTimeoutCutsUnprotectedStreams(t *testing.T) {
	srv := sseTestServer(t, 150*time.Millisecond, 100*time.Millisecond, 4,
		func(w gin.ResponseWriter, i int) error {
			_, err := io.WriteString(w, fmt.Sprintf("data:%d\n\n", i))
			return err
		})

	resp, err := http.Get(srv.URL + "/sse")
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr == nil && strings.Contains(string(body), "data:3") {
		t.Fatalf("control case was not cut: WriteTimeout did not apply, body=%q", body)
	}
}
