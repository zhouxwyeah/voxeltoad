// Package cache provides response caching behind an interface. The default
// implementation is in-memory with TTL; a Redis-backed implementation can be
// added for multi-instance deployments without changing call sites.
//
// See design/architecture.md: Redis is an opt-in scaling cost.
package cache

import (
	"context"
	"sync"
	"time"
)

// Cache is a minimal key/value store with TTL. Implementations must be safe for
// concurrent use.
type Cache interface {
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

type entry struct {
	value     []byte
	expiresAt time.Time
}

// MemoryCache is an in-process TTL cache. Expired entries are removed lazily on
// access.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]entry
	now     func() time.Time // injectable for tests
}

// NewMemoryCache returns an empty in-memory cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		entries: make(map[string]entry),
		now:     time.Now,
	}
}

// Get returns the value for key if present and not expired.
func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if c.now().After(e.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false, nil
	}
	return e.value, true, nil
}

// Set stores value under key for the given ttl.
func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	c.entries[key] = entry{value: value, expiresAt: c.now().Add(ttl)}
	c.mu.Unlock()
	return nil
}
