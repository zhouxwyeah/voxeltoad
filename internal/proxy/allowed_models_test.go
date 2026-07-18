package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/auth"
	"voxeltoad/internal/proxy"
)

// A key restricted via AllowedModels may only request those model aliases; any
// other alias is rejected 403 before dispatch (authorization, not auth).
func TestAllowedModels_DeniesDisallowedModel(t *testing.T) {
	rec := auth.KeyRecord{
		KeyID: "key_limited", Tenant: "acme", Hash: keyHash("sk-limited"),
		AllowedModels: []string{"chat"},
	}
	a := newAuthn(t, map[string]auth.KeyRecord{rec.Hash: rec})
	h := proxy.Router(nil, proxy.WithAuth(a))

	// Requesting a model not in AllowedModels → 403.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"forbidden","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-limited")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for disallowed model; body=%s", rr.Code, rr.Body.String())
	}
}

// The same key may request a model that IS in its AllowedModels list; it passes
// authorization (and hits the nil-dispatcher 501, i.e. it got past the model
// gate).
func TestAllowedModels_AllowsListedModel(t *testing.T) {
	rec := auth.KeyRecord{
		KeyID: "key_limited", Tenant: "acme", Hash: keyHash("sk-limited"),
		AllowedModels: []string{"chat"},
	}
	a := newAuthn(t, map[string]auth.KeyRecord{rec.Hash: rec})
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"chat","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-limited")
	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Fatalf("listed model was rejected 403; body=%s", rr.Body.String())
	}
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (past model gate, nil dispatcher); body=%s", rr.Code, rr.Body.String())
	}
}

// An empty AllowedModels means "all models allowed" — no restriction.
func TestAllowedModels_EmptyMeansAll(t *testing.T) {
	rec := auth.KeyRecord{KeyID: "key_all", Tenant: "acme", Hash: keyHash("sk-all")} // no AllowedModels
	a := newAuthn(t, map[string]auth.KeyRecord{rec.Hash: rec})
	h := proxy.Router(nil, proxy.WithAuth(a))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"anything","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-all")
	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Fatalf("empty AllowedModels must allow any model; got 403 body=%s", rr.Body.String())
	}
}
