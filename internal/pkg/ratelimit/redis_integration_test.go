package ratelimit

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestRedisIntegration verifies the distributed backend against a live Redis.
// It is skipped unless RGX_REDIS_TEST_ADDR is set, e.g.:
//
//	RGX_REDIS_TEST_ADDR=192.168.4.153:6389 \
//	RGX_REDIS_TEST_USERNAME=default \
//	RGX_REDIS_TEST_PASSWORD=admin123456 \
//	RGX_REDIS_TEST_DB=0  go test ./internal/pkg/ratelimit/ -run TestRedisIntegration -v
func TestRedisIntegration(t *testing.T) {
	addr := os.Getenv("RGX_REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("RGX_REDIS_TEST_ADDR not set; skipping live Redis test")
	}
	cfg := func() RedisConfig {
		db, _ := strconv.Atoi(os.Getenv("RGX_REDIS_TEST_DB"))
		return RedisConfig{
			Addr:     addr,
			Username: os.Getenv("RGX_REDIS_TEST_USERNAME"),
			Password: os.Getenv("RGX_REDIS_TEST_PASSWORD"),
			DB:       db,
			PoolSize: 5,
		}
	}

	rl, err := NewRedis(cfg())
	if err != nil {
		t.Fatalf("connect to Redis failed: %v", err)
	}
	defer rl.Close()

	ctx := context.Background()
	key := "itest:ip:1."

	// Two "instances" share the same Redis keys, proving distributed counting.
	rl2, err := NewRedis(cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer rl2.Close()

	var last int64
	for i := 0; i < 3; i++ {
		n, err := rl.Incr(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		last = n
	}
	if last != 3 {
		t.Fatalf("expected 3 hits on instance 1, got %d", last)
	}

	// A different client process must observe the same counter (multi-replica).
	if n, err := rl2.Count(ctx, key, time.Minute); err != nil || n != 3 {
		t.Fatalf("instance 2 should see 3 hits, got %d (err=%v)", n, err)
	}

	if err := rl.Reset(ctx, key); err != nil {
		t.Fatal(err)
	}
	if n, _ := rl.Count(ctx, key, time.Minute); n != 0 {
		t.Fatalf("expected 0 after reset, got %d", n)
	}
}
