// Package ratelimit provides sliding-window rate limiting with a pluggable
// backend. The default Memory backend is process-local and adequate for single
// instances; the Redis backend scales the same semantics across replicas.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter tracks occurrence counts within a sliding time window per key.
type Limiter interface {
	// Count returns how many occurrences for key are within window.
	Count(ctx context.Context, key string, window time.Duration) (int64, error)
	// Incr records one occurrence for key and returns the in-window count.
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
	// Reset clears all occurrences for key.
	Reset(ctx context.Context, key string) error
	// Close releases backend resources (no-op for the memory backend).
	Close() error
}

// Memory is a process-local sliding-window limiter. It is the default and
// requires no external dependency, but does not coordinate across instances.
type Memory struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// NewMemory builds a process-local limiter.
func NewMemory() *Memory {
	return &Memory{hits: map[string][]time.Time{}}
}

func (m *Memory) prune(key string, window time.Duration) {
	cutoff := time.Now().Add(-window)
	recent := m.hits[key][:0]
	for _, t := range m.hits[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	m.hits[key] = recent
	if len(recent) == 0 {
		delete(m.hits, key)
	}
}

func (m *Memory) Count(_ context.Context, key string, window time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(key, window)
	return int64(len(m.hits[key])), nil
}

func (m *Memory) Incr(_ context.Context, key string, window time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(key, window)
	m.hits[key] = append(m.hits[key], time.Now())
	return int64(len(m.hits[key])), nil
}

func (m *Memory) Reset(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hits, key)
	return nil
}

// Close is a no-op for the memory backend.
func (m *Memory) Close() error { return nil }
