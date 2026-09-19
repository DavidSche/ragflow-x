package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
)

func TestRateLimitRejectsOverLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := ratelimit.NewMemory()
	r := gin.New()
	r.GET("/x", RateLimit(limiter, ClientIPKey, 2, time.Minute), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// First two calls pass, the third is rate limited.
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d expected 200, got %d", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
}

func TestRateLimitBearerKeyIsolatesTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := ratelimit.NewMemory()
	r := gin.New()
	r.POST("/gw", RateLimit(limiter, BearerKey, 1, time.Minute), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	call := func(token string, remoteIP string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/gw", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.RemoteAddr = remoteIP + ":1234"
		r.ServeHTTP(w, req)
		return w.Code
	}

	// Two different keys from the SAME egress IP get independent buckets.
	if code := call("key-a", "9.9.9.9"); code != http.StatusOK {
		t.Fatalf("key-a first call expected 200, got %d", code)
	}
	if code := call("key-b", "9.9.9.9"); code != http.StatusOK {
		t.Fatalf("key-b first call expected 200 (independent bucket), got %d", code)
	}
	// Second call with key-a from a DIFFERENT IP still shares the key bucket.
	if code := call("key-a", "8.8.8.8"); code != http.StatusTooManyRequests {
		t.Fatalf("key-a over-limit expected 429 regardless of IP, got %d", code)
	}
	// No token falls back to client IP bucket.
	if code := call("", "7.7.7.7"); code != http.StatusOK {
		t.Fatalf("anonymous first call expected 200, got %d", code)
	}
	if code := call("", "7.7.7.7"); code != http.StatusTooManyRequests {
		t.Fatalf("anonymous over-limit expected 429, got %d", code)
	}
}

// errLimiter simulates an unavailable limiter backend (e.g. Redis down) so the
// middleware must fail open and still serve the request (doc/08 observability).
type errLimiter struct{}

func (errLimiter) Count(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("limiter backend down")
}
func (errLimiter) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("limiter backend down")
}
func (errLimiter) Reset(context.Context, string) error { return errors.New("limiter backend down") }
func (errLimiter) Close() error                        { return nil }

func TestRateLimitFailsOpenWhenLimiterUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/failopen", RateLimit(errLimiter{}, ClientIPKey, 1, time.Minute), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/failopen", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (fail open on limiter error), got %d", w.Code)
	}
}

func TestRateLimitStrictFailsClosedWhenLimiterUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/strict", RateLimitStrict(errLimiter{}, ClientIPKey, 1, time.Minute), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/strict", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (fail closed on limiter error), got %d", w.Code)
	}
}
