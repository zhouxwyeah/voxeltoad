package admin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeProvider_StatusClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantOK     bool
		wantErrSub string
	}{
		{"ok 200", http.StatusOK, `{"data":[]}`, true, ""},
		{"reachable 404 (no /models endpoint)", http.StatusNotFound, "not found", true, ""},
		{"reachable 429", http.StatusTooManyRequests, "slow down", true, ""},
		{"auth 401", http.StatusUnauthorized, `{"error":"bad key"}`, false, "authentication failed"},
		{"auth 403", http.StatusForbidden, "denied", false, "authentication failed"},
		{"server 500", http.StatusInternalServerError, "boom", false, "upstream server error (500)"},
		{"server 502", http.StatusBadGateway, "boom", false, "upstream server error (502)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Errorf("probe path = %q, want /v1/models", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			res := probeProvider(context.Background(), "openai", srv.URL+"/v1", "k")
			if res.OK != tc.wantOK {
				t.Fatalf("ok = %v, want %v (error=%q)", res.OK, tc.wantOK, res.Error)
			}
			if res.Status != tc.status {
				t.Errorf("status = %d, want %d", res.Status, tc.status)
			}
			if tc.wantErrSub != "" && !strings.Contains(res.Error, tc.wantErrSub) {
				t.Errorf("error = %q, want substring %q", res.Error, tc.wantErrSub)
			}
			if !tc.wantOK && tc.body != "" && !strings.Contains(res.Error, tc.body) {
				t.Errorf("error = %q, want body snippet %q", res.Error, tc.body)
			}
		})
	}
}

func TestProbeProvider_AuthHeaders(t *testing.T) {
	cases := []struct {
		name             string
		adapter          string
		apiKey           string
		wantAuth         string
		wantXAPIKey      string
		wantAnthropicVer string
	}{
		{"openai bearer", "openai", "sk-1", "Bearer sk-1", "", ""},
		{"unknown adapter defaults to bearer", "acme", "sk-2", "Bearer sk-2", "", ""},
		{"claude x-api-key", "claude", "sk-ant", "", "sk-ant", "2023-06-01"},
		{"no key no auth headers", "openai", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != tc.wantAuth {
					t.Errorf("Authorization = %q, want %q", got, tc.wantAuth)
				}
				if got := r.Header.Get("x-api-key"); got != tc.wantXAPIKey {
					t.Errorf("x-api-key = %q, want %q", got, tc.wantXAPIKey)
				}
				if got := r.Header.Get("anthropic-version"); got != tc.wantAnthropicVer {
					t.Errorf("anthropic-version = %q, want %q", got, tc.wantAnthropicVer)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			res := probeProvider(context.Background(), tc.adapter, srv.URL, tc.apiKey)
			if !res.OK {
				t.Fatalf("ok = false, error = %q", res.Error)
			}
		})
	}
}

func TestProbeProvider_TrailingSlashTrimmed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("probe path = %q, want /v1/models", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := probeProvider(context.Background(), "openai", srv.URL+"/v1/", "k")
	if !res.OK {
		t.Fatalf("ok = false, error = %q", res.Error)
	}
}

func TestProbeProvider_Unreachable(t *testing.T) {
	// Bind then close to get a definitely-closed port: the dial is refused
	// immediately instead of hanging until the probe timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	res := probeProvider(context.Background(), "openai", "http://"+addr, "k")
	if res.OK {
		t.Fatal("ok = true, want false for closed port")
	}
	if !strings.Contains(res.Error, "unreachable") {
		t.Errorf("error = %q, want prefix %q", res.Error, "unreachable")
	}
}

func TestProbeProvider_InvalidBaseURL(t *testing.T) {
	for _, raw := range []string{"not-a-url", "http://", "://bad"} {
		res := probeProvider(context.Background(), "openai", raw, "k")
		if res.OK {
			t.Errorf("base_url %q: ok = true, want false", raw)
		}
		if res.Error != "invalid base_url" {
			t.Errorf("base_url %q: error = %q, want %q", raw, res.Error, "invalid base_url")
		}
	}
}

func TestIsMaskedRef(t *testing.T) {
	masked := []string{"***", "env://***", "plain://***"}
	for _, ref := range masked {
		if !isMaskedRef(ref) {
			t.Errorf("isMaskedRef(%q) = false, want true", ref)
		}
	}
	real := []string{"", "env://OPENAI_KEY", "plain://secret", "db://provider/p1", "sk-bare"}
	for _, ref := range real {
		if isMaskedRef(ref) {
			t.Errorf("isMaskedRef(%q) = true, want false", ref)
		}
	}
}

func TestResolveConnRequestKey_NoStore(t *testing.T) {
	t.Setenv("PROBE_TEST_KEY", "env-secret")

	// Plaintext api_key always wins; the store is never consulted (nil repo).
	key, err := resolveConnRequestKey(context.Background(), providerConnRequest{APIKey: "plain-k"}, nil, nil, nil)
	if err != nil || key != "plain-k" {
		t.Errorf("api_key: key = %q, err = %v; want plain-k, nil", key, err)
	}

	// Resolvable env:// ref.
	key, err = resolveConnRequestKey(context.Background(), providerConnRequest{APIKeyRef: "env://PROBE_TEST_KEY"}, nil, nil, nil)
	if err != nil || key != "env-secret" {
		t.Errorf("env ref: key = %q, err = %v; want env-secret, nil", key, err)
	}

	// Unresolvable ref without a name to fall back on surfaces the error.
	_, err = resolveConnRequestKey(context.Background(), providerConnRequest{APIKeyRef: "env://PROBE_TEST_UNSET"}, nil, nil, nil)
	if err == nil {
		t.Error("unset env ref: err = nil, want resolution error")
	}

	// Masked placeholder without a name resolves to an empty key (no-auth probe).
	key, err = resolveConnRequestKey(context.Background(), providerConnRequest{APIKeyRef: "env://***"}, nil, nil, nil)
	if err != nil || key != "" {
		t.Errorf("masked ref: key = %q, err = %v; want empty, nil", key, err)
	}
}
