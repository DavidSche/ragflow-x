// Package routestate holds the short-TTL route execution state. It is
// intentionally separate from durable bootstrap references, which remain in
// the operational database.
package routestate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config mirrors the process Redis settings without importing service config.
type Config struct {
	Addr     string
	Username string
	Password string
	DB       int
	PoolSize int
}

// Store is the minimal state contract used by the conversation router.
type Store interface {
	Save(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Load(ctx context.Context, key string) ([]byte, bool, error)
	Close() error
}

// Redis is a JSON-friendly short TTL state store.
type Redis struct {
	client *redis.Client
}

// NewRedis verifies connectivity before returning the route state store.
func NewRedis(cfg Config) (*Redis, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis address is required")
	}
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = 10
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: poolSize,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &Redis{client: client}, nil
}

// Save writes a route execution object with the required TTL.
func (r *Redis) Save(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("route state ttl must be positive")
	}
	return r.client.Set(ctx, "ragflowx:route:"+key, value, ttl).Err()
}

// Load returns false when the short TTL object is missing or expired.
func (r *Redis) Load(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := r.client.Get(ctx, "ragflowx:route:"+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// Close releases the Redis connection pool.
func (r *Redis) Close() error { return r.client.Close() }
