package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
)

// brokenLimiter simulates a login limiter backend outage (e.g. Redis down).
// A6 / doc/08: login must fail open and stay observable instead of blocking
// every attempt when the limiter itself is unavailable.
type brokenLimiter struct{}

func (brokenLimiter) Count(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("limiter backend down")
}
func (brokenLimiter) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("limiter backend down")
}
func (brokenLimiter) Reset(context.Context, string) error { return errors.New("limiter backend down") }
func (brokenLimiter) Close() error                        { return nil }

func TestLoginPermittedFailsOpenWhenLimiterUnavailable(t *testing.T) {
	s := &Service{LoginLimiter: brokenLimiter{}}
	if !s.loginPermitted(context.Background(), "1.2.3.4") {
		t.Fatal("login must fail open when the limiter backend is unavailable")
	}
}

func TestLoginPermittedRejectsAfterLimit(t *testing.T) {
	s := &Service{LoginLimiter: ratelimit.NewMemory()}
	key := "10.0.0.1"
	for i := 0; i < loginMaxAttempts; i++ {
		if !s.loginPermitted(context.Background(), key) {
			t.Fatalf("attempt %d must be permitted while under limit", i+1)
		}
		s.recordLoginFailure(context.Background(), key)
	}
	if s.loginPermitted(context.Background(), key) {
		t.Fatal("attempt after reaching the limit must be rejected")
	}
}
