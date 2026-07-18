package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"voxeltoad/internal/auth"
)

func hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// fakeStore is an in-test KeyStore counting lookups so we can assert caching.
type fakeStore struct {
	byHash  map[string]auth.KeyRecord
	lookups int
}

func (s *fakeStore) LookupByHash(_ context.Context, h string) (auth.KeyRecord, bool, error) {
	s.lookups++
	rec, ok := s.byHash[h]
	return rec, ok, nil
}

func newAuth(t *testing.T, recs ...auth.KeyRecord) (*auth.Authenticator, *fakeStore) {
	t.Helper()
	store := &fakeStore{byHash: map[string]auth.KeyRecord{}}
	for _, r := range recs {
		store.byHash[r.Hash] = r
	}
	a := auth.NewAuthenticator(store, auth.Options{CacheTTL: time.Minute, NegativeTTL: time.Minute})
	return a, store
}

func TestAuthenticate_ValidKey(t *testing.T) {
	rec := auth.KeyRecord{
		KeyID:  "key_01H",
		Tenant: "acme",
		Group:  "team-a",
		Hash:   hash("sk-good"),
	}
	a, _ := newAuth(t, rec)

	got, err := a.Authenticate(context.Background(), "sk-good")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.KeyID != "key_01H" || got.Tenant != "acme" || got.Group != "team-a" {
		t.Errorf("record = %+v, want KeyID/Tenant/Group set", got)
	}
}

func TestAuthenticate_UnknownKeyRejected(t *testing.T) {
	a, _ := newAuth(t)
	_, err := a.Authenticate(context.Background(), "sk-nope")
	if !errors.Is(err, auth.ErrInvalidKey) {
		t.Errorf("err = %v, want ErrInvalidKey", err)
	}
}

func TestAuthenticate_EmptyKeyRejected(t *testing.T) {
	a, _ := newAuth(t)
	if _, err := a.Authenticate(context.Background(), ""); !errors.Is(err, auth.ErrInvalidKey) {
		t.Errorf("err = %v, want ErrInvalidKey", err)
	}
}

func TestAuthenticate_ExpiredKeyRejected(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	rec := auth.KeyRecord{KeyID: "key_x", Tenant: "acme", Hash: hash("sk-exp"), ExpiresAt: &past}
	a, _ := newAuth(t, rec)
	if _, err := a.Authenticate(context.Background(), "sk-exp"); !errors.Is(err, auth.ErrKeyExpired) {
		t.Errorf("err = %v, want ErrKeyExpired", err)
	}
}

// TestAuthenticate_CachesPositive: a second auth of the same key must not hit
// the store again within the cache TTL (ADR-0006 cache-first).
func TestAuthenticate_CachesPositive(t *testing.T) {
	rec := auth.KeyRecord{KeyID: "key_c", Tenant: "acme", Hash: hash("sk-cache")}
	a, store := newAuth(t, rec)

	for i := 0; i < 3; i++ {
		if _, err := a.Authenticate(context.Background(), "sk-cache"); err != nil {
			t.Fatalf("auth %d: %v", i, err)
		}
	}
	if store.lookups != 1 {
		t.Errorf("store lookups = %d, want 1 (cached)", store.lookups)
	}
}

// TestAuthenticate_CachesNegative: repeated unknown keys are not re-looked-up
// within NegativeTTL (blunts invalid-key floods, ADR-0006).
func TestAuthenticate_CachesNegative(t *testing.T) {
	a, store := newAuth(t)
	for i := 0; i < 3; i++ {
		_, _ = a.Authenticate(context.Background(), "sk-bad")
	}
	if store.lookups != 1 {
		t.Errorf("store lookups = %d, want 1 (negative cached)", store.lookups)
	}
}

// TestAuthenticate_PositiveCacheExpires: after the TTL, a fresh lookup occurs
// (so revocation takes effect within the TTL).
func TestAuthenticate_PositiveCacheExpires(t *testing.T) {
	rec := auth.KeyRecord{KeyID: "key_t", Tenant: "acme", Hash: hash("sk-ttl")}
	store := &fakeStore{byHash: map[string]auth.KeyRecord{rec.Hash: rec}}
	clk := time.Unix(1_000_000, 0)
	a := auth.NewAuthenticator(store, auth.Options{CacheTTL: time.Minute, NegativeTTL: time.Minute})
	a.SetClock(func() time.Time { return clk })

	_, _ = a.Authenticate(context.Background(), "sk-ttl")
	clk = clk.Add(2 * time.Minute) // past TTL
	_, _ = a.Authenticate(context.Background(), "sk-ttl")

	if store.lookups != 2 {
		t.Errorf("store lookups = %d, want 2 (cache expired → re-lookup)", store.lookups)
	}
}

// TestAuthenticate_NeverLogsOrReturnsHash: the returned record exposes a public
// KeyID, never the plaintext key or its hash in that field (ADR-0006).
func TestAuthenticate_ReturnsPublicKeyID(t *testing.T) {
	rec := auth.KeyRecord{KeyID: "key_pub", Tenant: "acme", Hash: hash("sk-id")}
	a, _ := newAuth(t, rec)
	got, _ := a.Authenticate(context.Background(), "sk-id")
	if got.KeyID != "key_pub" {
		t.Errorf("KeyID = %q, want key_pub", got.KeyID)
	}
	if got.KeyID == hash("sk-id") || got.KeyID == "sk-id" {
		t.Error("KeyID must be a public id, not the hash or plaintext")
	}
}
