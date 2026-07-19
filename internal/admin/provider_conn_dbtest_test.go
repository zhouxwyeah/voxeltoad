//go:build dbtest

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProviderConnTest covers both connectivity-test endpoints: probing a
// saved provider (credential resolved server-side) and probing unsaved ad-hoc
// form values (plaintext key / stored-credential fallback), plus validation
// and auth failures. The upstream is an httptest stub that only answers
// /v1/models for the right Bearer key.
func TestProviderConnTest(t *testing.T) {
	h, _, tok := authedAdmin(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret-k" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad key"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()
	baseURL := upstream.URL + "/v1"

	// Unauthenticated → 401 (super-admin group).
	if rr := do(t, h, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"adapter": "openai", "base_url": baseURL,
	}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rr.Code)
	}

	// Unknown saved provider → 404.
	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers/ghost/test", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("ghost test: status = %d, want 404", rr.Code)
	}

	// Create a provider with a plaintext key: it is stored encrypted and the
	// ref becomes db://provider/p1 (ADR-0030/0031).
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "openai", "adapter": "openai",
		"base_url": baseURL, "api_key": "secret-k",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// Probe the saved provider: the db-stored credential is decrypted and used.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers/p1/test", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("saved test: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	res := decodeOne(t, rr)
	if res["ok"] != true || res["status"] != float64(http.StatusOK) {
		t.Errorf("saved test: got %v, want ok=true status=200", res)
	}
	if _, hasLatency := res["latency_ms"]; !hasLatency {
		t.Errorf("saved test: missing latency_ms in %v", res)
	}

	// Ad-hoc probe with an inline plaintext key.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"adapter": "openai", "base_url": baseURL, "api_key": "secret-k",
	})
	if res := decodeOne(t, rr); rr.Code != http.StatusOK || res["ok"] != true {
		t.Errorf("ad-hoc key: status = %d, got %v, want 200 ok=true", rr.Code, res)
	}

	// Ad-hoc probe with only a name falls back to the stored credential.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"name": "p1", "adapter": "openai", "base_url": baseURL,
	})
	if res := decodeOne(t, rr); rr.Code != http.StatusOK || res["ok"] != true {
		t.Errorf("ad-hoc name fallback: status = %d, got %v, want 200 ok=true", rr.Code, res)
	}

	// Ad-hoc probe with a masked ref (edit modal) also falls back to the
	// stored credential.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"name": "p1", "adapter": "openai", "base_url": baseURL, "api_key_ref": "env://***",
	})
	if res := decodeOne(t, rr); rr.Code != http.StatusOK || res["ok"] != true {
		t.Errorf("ad-hoc masked ref: status = %d, got %v, want 200 ok=true", rr.Code, res)
	}

	// Wrong key → 200 with ok=false and an auth-failure reason.
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"adapter": "openai", "base_url": baseURL, "api_key": "wrong",
	})
	res = decodeOne(t, rr)
	if rr.Code != http.StatusOK || res["ok"] != false {
		t.Fatalf("wrong key: status = %d, got %v, want 200 ok=false", rr.Code, res)
	}
	if msg, _ := res["error"].(string); !strings.Contains(msg, "authentication failed") {
		t.Errorf("wrong key: error = %q, want auth failure", msg)
	}

	// Validation: unknown adapter and missing base_url → 400.
	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"adapter": "nope", "base_url": baseURL,
	}); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown adapter: status = %d, want 400", rr.Code)
	}
	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/provider-tests", map[string]any{
		"adapter": "openai",
	}); rr.Code != http.StatusBadRequest {
		t.Errorf("missing base_url: status = %d, want 400", rr.Code)
	}
}
