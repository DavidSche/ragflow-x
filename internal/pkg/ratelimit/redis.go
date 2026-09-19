package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig configures the distributed limiter.
type RedisConfig struct {
	Addr     string
	Username string
	Password string
	DB       int
	PoolSize int
}

// Redis is a distributed sliding-window limiter backed by a Redis sorted set
// (member = occurrence id, score = unix milliseconds). Keys expire shortly
// after the window passes so stale state does not accumulate.
type Redis struct {
	cli *redis.Client
	seq int64
}

// NewRedis builds a Redis-backed limiter and verifies connectivity.
func NewRedis(cfg RedisConfig) (*Redis, error) {
	addr := cfg.Addr
	if addr == "" {
		addr = "localhost:6379"
	}
	pool := cfg.PoolSize
	if pool <= 0 {
		pool = 10
	}
	cli := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: cfg.Password,
		Username: cfg.Username,
		DB:       cfg.DB,
		PoolSize: pool,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := cli.Ping(ctx).Err(); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &Redis{cli: cli}, nil
}

func rlKey(k string) string { return "ragflowx:rl:" + k }

func (r *Redis) prune(ctx context.Context, redisKey string, window time.Duration) error {
	min := strconv.FormatInt(time.Now().Add(-window).UnixMilli(), 10)
	return r.cli.ZRemRangeByScore(ctx, redisKey, "0", min).Err()
}

func (r *Redis) Count(ctx context.Context, key string, window time.Duration) (int64, error) {
	redisKey := rlKey(key)
	if err := r.prune(ctx, redisKey, window); err != nil {
		return 0, err
	}
	return r.cli.ZCard(ctx, redisKey).Result()
}

func (r *Redis) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	redisKey := rlKey(key)
	if err := r.prune(ctx, redisKey, window); err != nil {
		return 0, err
	}
	member := r.member()
	if err := r.cli.ZAdd(ctx, redisKey, redis.Z{Score: float64(time.Now().UnixMilli()), Member: member}).Err(); err != nil {
		return 0, err
	}
	if err := r.cli.Expire(ctx, redisKey, window+time.Minute).Err(); err != nil {
		return 0, err
	}
	return r.cli.ZCard(ctx, redisKey).Result()
}

func (r *Redis) Reset(ctx context.Context, key string) error {
	return r.cli.Del(ctx, rlKey(key)).Err()
}

func (r *Redis) Close() error {
	if r.cli == nil {
		return nil
	}
	return r.cli.Close()
}

func (r *Redis) member() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%d-%s", atomic.AddInt64(&r.seq, 1), hex.EncodeToString(buf))
}
