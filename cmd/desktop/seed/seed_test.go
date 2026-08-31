package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"voxeltoad/internal/desktopstore"
)

func newTestDB(t *testing.T) *desktopstore.DB {
	t.Helper()
	db, err := desktopstore.Open(filepath.Join(t.TempDir(), "test.db"))
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

// storedHash reads the seeded "default" row's hash directly, so the tests pin
// seed behavior without depending on the keystore API under change.
func storedHash(t *testing.T, db *desktopstore.DB) string {
	t.Helper()
	var row desktopstore.APIKeyRow
	if err := db.Where(desktopstore.APIKeyRow{KeyID: "default"}).First(&row).Error; err != nil {
		t.Fatalf("load default key row: %v", err)
	}
	return row.Hash
}

func TestKey_CreatesWhenMissing(t *testing.T) {
	db := newTestDB(t)
	if err := Key(context.Background(), db, "startup-key", false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got, want := storedHash(t, db), hashOf("startup-key"); got != want {
		t.Errorf("stored hash = %s, want %s", got, want)
	}
}

// The regression that motivated the overwrite flag: a key rotated via the
// settings UI must survive restarts. Re-seeding with the built-in default
// plaintext (overwrite=false) must not clobber the rotated hash.
func TestKey_DoesNotOverwriteRotatedHash(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := Key(ctx, db, "startup-key", false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ks := desktopstore.NewKeyStore(db)
	if err := ks.RotateDefaultKey(ctx, hashOf("dt-sk-rotated")); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// Restart: seed runs again with the same startup plaintext.
	if err := Key(ctx, db, "startup-key", false); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if got, want := storedHash(t, db), hashOf("dt-sk-rotated"); got != want {
		t.Errorf("re-seed overwrote rotated hash: got %s, want %s", got, want)
	}
}

// An explicitly set GATEWAY_DESKTOP_KEY is the operator's deliberate choice
// for this process: it replaces a divergent stored hash (e.g. to recover a
// lost rotated key) and wins over the previous rotation.
func TestKey_EnvOverwriteWinsOverRotation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := Key(ctx, db, "old-env-key", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ks := desktopstore.NewKeyStore(db)
	if err := ks.RotateDefaultKey(ctx, hashOf("dt-sk-rotated")); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := Key(ctx, db, "new-env-key", true); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if got, want := storedHash(t, db), hashOf("new-env-key"); got != want {
		t.Errorf("env overwrite did not win: got %s, want %s", got, want)
	}
}

// Overwriting with the same plaintext is a no-op (hash already matches).
func TestKey_EnvOverwriteSameHashIsNoop(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := Key(ctx, db, "env-key", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Key(ctx, db, "env-key", true); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if got, want := storedHash(t, db), hashOf("env-key"); got != want {
		t.Errorf("stored hash = %s, want %s", got, want)
	}
}

func TestKeyMatches(t *testing.T) {
	if !KeyMatches("plain", hashOf("plain")) {
		t.Error("expected match for identical plaintext")
	}
	if KeyMatches("plain", hashOf("other")) {
		t.Error("expected mismatch for different plaintext")
	}
}
