package routestate

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRedisStoreRoundTrip(t *testing.T) {
	addr := os.Getenv("RGX_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set RGX_TEST_REDIS_ADDR to run the integration check")
	}
	store, err := NewRedis(Config{
		Addr:     addr,
		Username: os.Getenv("RGX_TEST_REDIS_USERNAME"),
		Password: os.Getenv("RGX_TEST_REDIS_PASSWORD"),
		DB:       0,
		PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	key := "m25-integration-test"
	if err := store.Save(ctx, key, []byte("ok"), time.Second); err != nil {
		t.Fatalf("save: %v", err)
	}
	value, found, err := store.Load(ctx, key)
	if err != nil || !found || string(value) != "ok" {
		t.Fatalf("load: found=%v value=%q err=%v", found, value, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, found, err := store.Load(ctx, key); err != nil || found {
		t.Fatalf("ttl expiry: found=%v err=%v", found, err)
	}
}
