// Package auth authenticates client API keys for the data plane. Keys are
// stored only as hashes (never plaintext); authentication hashes the presented
// key and looks up the record. Lookups are cache-first with a fallback to a
// KeyStore on miss, so keys stay real-time without bloating the config snapshot
// (see ADR-0006). The three-level tenancy Tenant → Group → APIKey comes from
// ADR-0005.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by Authenticate.
var (
	// ErrInvalidKey means the key is empty or unknown.
	ErrInvalidKey = errors.New("auth: invalid api key")
	// ErrKeyExpired means the key exists but has passed its expiry.
	ErrKeyExpired = errors.New("auth: api key expired")
)

// KeyRecord is the resolved metadata for an API key. The plaintext key is never
// stored; Hash is its SHA-256 hex digest. KeyID is a public, human-readable
// identifier safe for logs/audit (llm.api_key_id).
type KeyRecord struct {
	KeyID     string
	Tenant    string
	Group     string
	Hash      string
	ExpiresAt *time.Time // nil = no expiry
	// AllowedModels optionally restricts model aliases (empty = all). Enforced
	// by the auth/authorization step that consumes the record.
	AllowedModels []string
}

// KeyStore is the authoritative (slower) lookup the Authenticator falls back to
// on a cache miss. Implementations are backed by the management plane / DB.
type KeyStore interface {
	// LookupByHash returns the record for a key hash. ok is false if no such
	// key exists.
	LookupByHash(ctx context.Context, hash string) (KeyRecord, bool, error)
}

// Options configures the Authenticator's caching.
type Options struct {
	// CacheTTL bounds how long a positive lookup is cached (and thus the
	// worst-case revocation delay).
	CacheTTL time.Duration
	// NegativeTTL bounds how long an unknown-key result is cached (to blunt
	// invalid-key floods).
	NegativeTTL time.Duration
}

type cacheEntry struct {
	rec      KeyRecord
	ok       bool // false = negative (unknown key) cache entry
	expireAt time.Time
}

// Authenticator resolves API keys with a cache-first strategy.
type Authenticator struct {
	store       KeyStore
	cacheTTL    time.Duration
	negativeTTL time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry // keyed by hash
	now   func() time.Time
}

// NewAuthenticator builds an Authenticator over the given store.
func NewAuthenticator(store KeyStore, opts Options) *Authenticator {
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = time.Minute
	}
	if opts.NegativeTTL <= 0 {
		opts.NegativeTTL = opts.CacheTTL
	}
	return &Authenticator{
		store:       store,
		cacheTTL:    opts.CacheTTL,
		negativeTTL: opts.NegativeTTL,
		cache:       make(map[string]cacheEntry),
		now:         time.Now,
	}
}

// SetClock overrides the time source (tests only).
func (a *Authenticator) SetClock(now func() time.Time) { a.now = now }

// Authenticate validates a plaintext key and returns its record. It hashes the
// key, consults the cache, and falls back to the store on a miss.
func (a *Authenticator) Authenticate(ctx context.Context, plaintextKey string) (KeyRecord, error) {
	if plaintextKey == "" {
		return KeyRecord{}, ErrInvalidKey
	}
	h := hashKey(plaintextKey)

	rec, ok, err := a.resolve(ctx, h)
	if err != nil {
		return KeyRecord{}, err
	}
	if !ok {
		return KeyRecord{}, ErrInvalidKey
	}
	if rec.ExpiresAt != nil && !a.now().Before(*rec.ExpiresAt) {
		return KeyRecord{}, ErrKeyExpired
	}
	return rec, nil
}

// resolve returns a record from cache or store, caching the result.
func (a *Authenticator) resolve(ctx context.Context, h string) (KeyRecord, bool, error) {
	now := a.now()

	a.mu.Lock()
	if e, hit := a.cache[h]; hit && now.Before(e.expireAt) {
		a.mu.Unlock()
		return e.rec, e.ok, nil
	}
	a.mu.Unlock()

	// Miss (or expired): consult the store.
	rec, ok, err := a.store.LookupByHash(ctx, h)
	if err != nil {
		return KeyRecord{}, false, err
	}

	ttl := a.negativeTTL
	if ok {
		ttl = a.cacheTTL
	}
	a.mu.Lock()
	a.cache[h] = cacheEntry{rec: rec, ok: ok, expireAt: now.Add(ttl)}
	a.mu.Unlock()

	return rec, ok, nil
}

func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
