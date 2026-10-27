// Package approvalcache stores process-shared approval policy snapshots.
package approvalcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/redis/go-redis/v9"
)

const keyPrefix = "ragflowx:approval-policy:"

// Config mirrors process Redis settings without importing config.
type Config struct {
	Addr     string
	Username string
	Password string
	DB       int
	PoolSize int
}

// Redis implements the service shared policy cache contract.
type Redis struct {
	client *redis.Client
}

// NewRedis verifies connectivity before returning a usable cache.
func NewRedis(cfg Config) (*Redis, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis address is required")
	}
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = 10
	}
	client := redis.NewClient(&redis.Options{
		Addr: cfg.Addr, Username: cfg.Username, Password: cfg.Password,
		DB: cfg.DB, PoolSize: poolSize,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &Redis{client: client}, nil
}

// LoadPolicies returns false when no cache entry exists.
func (r *Redis) LoadPolicies(ctx context.Context, key string) ([]model.ApprovalPolicy, bool, error) {
	raw, err := r.client.Get(ctx, keyPrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var policies []model.ApprovalPolicy
	if err := json.Unmarshal(raw, &policies); err != nil {
		return nil, false, fmt.Errorf("decode approval policy cache: %w", err)
	}
	return policies, true, nil
}

// SavePolicies writes the policy snapshot with the configured TTL.
func (r *Redis) SavePolicies(ctx context.Context, key string, policies []model.ApprovalPolicy, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("approval policy cache ttl must be positive")
	}
	raw, err := json.Marshal(policies)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, keyPrefix+key, raw, ttl).Err()
}

// Delete removes the policy snapshot after a policy mutation.
func (r *Redis) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, keyPrefix+key).Err()
}

// Close releases the Redis connection pool.
func (r *Redis) Close() error { return r.client.Close() }
