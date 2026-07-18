package desktopstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"
)

// newTestDB opens a fresh SQLite in a temp dir for each test. Each test seeds
// its own key rows so cases stay independent.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestKeyStore_LookupByHash_Hit(t *testing.T) {
	db := newTestDB(t)
	row := APIKeyRow{
		KeyID:         "default",
		Hash:          hashOf("plaintext-key"),
		Tenant:        "default",
		Group:         "default",
		AllowedModels: "[]",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	ks := NewKeyStore(db)
	rec, ok, err := ks.LookupByHash(context.Background(), hashOf("plaintext-key"))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if rec.KeyID != "default" || rec.Tenant != "default" || rec.Group != "default" {
		t.Errorf("rec = %+v, want default/default/default fields", rec)
	}
	if rec.Hash != hashOf("plaintext-key") {
		t.Errorf("hash mismatch")
	}
	if len(rec.AllowedModels) != 0 {
		t.Errorf("AllowedModels = %v, want empty (means all allowed)", rec.AllowedModels)
	}
}

func TestKeyStore_LookupByHash_Miss(t *testing.T) {
	db := newTestDB(t)
	ks := NewKeyStore(db)

	_, ok, err := ks.LookupByHash(context.Background(), hashOf("not-seeded"))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Fatal("expected miss for unknown hash, got hit")
	}
}

func TestKeyStore_LookupByHash_Expired(t *testing.T) {
	db := newTestDB(t)
	past := time.Now().Add(-time.Hour)
	row := APIKeyRow{
		KeyID:     "expired",
		Hash:      hashOf("expired-key"),
		ExpiresAt: &past,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	ks := NewKeyStore(db)
	_, ok, err := ks.LookupByHash(context.Background(), hashOf("expired-key"))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Fatal("expired key should not be returned")
	}
}

func TestKeyStore_LookupByHash_Revoked(t *testing.T) {
	db := newTestDB(t)
	// revoked_at non-null → filtered by the WHERE clause
	now := time.Now()
	row := APIKeyRow{
		KeyID:     "revoked",
		Hash:      hashOf("revoked-key"),
		RevokedAt: &now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	ks := NewKeyStore(db)
	_, ok, err := ks.LookupByHash(context.Background(), hashOf("revoked-key"))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Fatal("revoked key should not be returned")
	}
}

func TestKeyStore_LookupByHash_EmptyAllowedModelsMeansAll(t *testing.T) {
	// K1 decision (design/desktop.md §8 / ADR-0041): the seeded default key has
	// empty AllowedModels, which the data plane's modelAllowed gate interprets
	// as "all models pass" (auth_middleware.go: len==0 => unrestricted). This
	// test pins that the keystore returns an empty slice for the "[]" JSON
	// payload so the gate sees len==0.
	db := newTestDB(t)
	row := APIKeyRow{
		KeyID:         "default",
		Hash:          hashOf("default-key"),
		AllowedModels: "[]",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	ks := NewKeyStore(db)
	rec, ok, err := ks.LookupByHash(context.Background(), hashOf("default-key"))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatal("expected hit")
	}

	// The data plane gate: empty AllowedModels = unrestricted. Pin len==0
	// here at the store boundary (the proxy.modelAllowed check lives in
	// internal/proxy and consumes this KeyRecord).
	if len(rec.AllowedModels) != 0 {
		t.Errorf("AllowedModels=%v, want empty slice so modelAllowed grants all (K1)",
			rec.AllowedModels)
	}
}
