package desktopapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	desktopseed "voxeltoad/cmd/desktop/seed"
	"voxeltoad/internal/desktopstore"
)

func TestAPIKey_GetAndRotate(t *testing.T) {
	db := openTestDB(t)
	if err := desktopseed.Key(context.Background(), db, desktopseed.DefaultKey()); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	ks := NewKeyState(desktopseed.DefaultKey())
	ts := httptest.NewServer(New(db, "", nil, nil, ks).Handler())
	defer ts.Close()

	// GET returns the known plaintext.
	code, b := getBody(t, ts, "/api/v1/apikey")
	if code != 200 {
		t.Fatalf("GET apikey: %d %s", code, b)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["key"] != desktopseed.DefaultKey() || got["plaintext_known"] != true {
		t.Errorf("GET apikey = %v, want the seeded key marked known", got)
	}

	// Rotate returns a fresh key and updates the shared state.
	resp, err := http.Post(ts.URL+"/api/v1/apikey/rotate", "application/json", nil)
	if err != nil {
		t.Fatalf("POST rotate: %v", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("rotate: %d %s", resp.StatusCode, rb)
	}
	var rotated struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rb, &rotated)
	if !strings.HasPrefix(rotated.Key, "dt-sk-") || len(rotated.Key) != 6+32 {
		t.Errorf("rotated key %q malformed (want dt-sk-<32 hex>)", rotated.Key)
	}
	if plaintext, known := ks.Get(); !known || plaintext != rotated.Key {
		t.Errorf("KeyState not updated after rotate")
	}

	// The stored hash authenticates the NEW key and rejects the old one.
	store := desktopstore.NewKeyStore(db)
	sum := sha256.Sum256([]byte(rotated.Key))
	if _, ok, err := store.LookupByHash(context.Background(), hex.EncodeToString(sum[:])); err != nil || !ok {
		t.Errorf("new key should authenticate (ok=%v err=%v)", ok, err)
	}
	oldSum := sha256.Sum256([]byte(desktopseed.DefaultKey()))
	if _, ok, err := store.LookupByHash(context.Background(), hex.EncodeToString(oldSum[:])); err != nil || ok {
		t.Errorf("old key should no longer authenticate (ok=%v err=%v)", ok, err)
	}
}

func TestAPIKey_UnavailableWithoutState(t *testing.T) {
	db := openTestDB(t)
	ts := httptest.NewServer(New(db, "", nil, nil, nil).Handler())
	defer ts.Close()
	if code, _ := getBody(t, ts, "/api/v1/apikey"); code != 503 {
		t.Errorf("GET apikey = %d, want 503 without key state", code)
	}
}
