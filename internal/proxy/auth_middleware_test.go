package proxy_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"voxeltoad/internal/auth"
	"voxeltoad/internal/proxy"
)

func keyHash(k string) string {
	s := sha256.Sum256([]byte(k))
	return hex.EncodeToString(s[:])
}

type memKeyStore struct{ recs map[string]auth.KeyRecord }

func (s memKeyStore) LookupByHash(_ context.Context, h string) (auth.KeyRecord, bool, error) {
	r, ok := s.recs[h]
	return r, ok, nil
}

func newAuthn(t *testing.T, keys map[string]auth.KeyRecord) *auth.Authenticator {
	t.Helper()
	return auth.NewAuthenticator(memKeyStore{recs: keys}, auth.Options{CacheTTL: time.Minute})
}

func TestAuthMiddleware_MissingHeaderIs401(t *testing.T) {
	a := newAuthn(t, nil)
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuthMiddleware_MalformedHeaderIs401(t *testing.T) {
	a := newAuthn(t, nil)
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	req.Header.Set("Authorization", "Token abc") // not "Bearer"
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_InvalidKeyIs401(t *testing.T) {
	a := newAuthn(t, map[string]auth.KeyRecord{})
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	req.Header.Set("Authorization", "Bearer sk-unknown")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_ValidKeyReachesHandler(t *testing.T) {
	rec := auth.KeyRecord{KeyID: "key_ok", Tenant: "acme", Group: "team", Hash: keyHash("sk-ok")}
	a := newAuthn(t, map[string]auth.KeyRecord{rec.Hash: rec})

	// No forwarder → valid key should pass auth and reach the (nil-forwarder)
	// handler, which returns 501. The point: it's past auth, not 401.
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	req.Header.Set("Authorization", "Bearer sk-ok")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (past auth, nil forwarder); body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuthMiddleware_HealthzNotProtected(t *testing.T) {
	a := newAuthn(t, nil)
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("healthz should be open, got %d", rr.Code)
	}
}

func TestRouter_NoAuthOption_StillOpen(t *testing.T) {
	// Without WithAuth, existing behavior is preserved (no 401).
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	proxy.Router(nil).ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Error("router without WithAuth must not require auth")
	}
}
