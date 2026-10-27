package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemorySlidingWindow(t *testing.T) {
	ctx := context.Background()
	l := NewMemory()

	for i := 0; i < 3; i++ {
		if _, err := l.Incr(ctx, "10.0.0.1", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	n, err := l.Count(ctx, "10.0.0.1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3 hits, got %d", n)
	}

	if err := l.Reset(ctx, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	n, _ = l.Count(ctx, "10.0.0.1", time.Minute)
	if n != 0 {
		t.Fatalf("expected 0 after reset, got %d", n)
	}
}

func TestMemoryExpiresOldHits(t *testing.T) {
	ctx := context.Background()
	l := NewMemory()

	if _, err := l.Incr(ctx, "ip", time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	n, _ := l.Count(ctx, "ip", time.Nanosecond)
	if n != 0 {
		t.Fatalf("expected old hits to expire, got %d", n)
	}
}
