package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshot_RequiresInternalToken(t *testing.T) {
	h := Router(Options{InternalToken: "s3cret"})

	// No token → 401.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/internal/config/snapshot", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rr.Code)
	}

	// Wrong token → 401.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal/config/snapshot", nil)
	req.Header.Set(InternalTokenHeader, "wrong")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rr.Code)
	}

	// Correct token → 200.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/internal/config/snapshot", nil)
	req.Header.Set(InternalTokenHeader, "s3cret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSnapshot_NoTokenConfigured_StaysOpen(t *testing.T) {
	// Empty InternalToken disables the gate (dev/test convenience).
	h := Router(Options{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/internal/config/snapshot", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when no token configured", rr.Code)
	}
}

func TestHealthz_NotGated(t *testing.T) {
	h := Router(Options{InternalToken: "s3cret"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("healthz must stay open, got %d", rr.Code)
	}
}

// When AllowedOrigins is configured, a matching Origin gets CORS headers and a
// preflight (OPTIONS) short-circuits to 204 (ADR-0019: front-end/back-end
// separation).
func TestCORS_AllowsConfiguredOrigin(t *testing.T) {
	h := Router(Options{AllowedOrigins: []string{"https://ui.example"}})

	// Preflight.
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/providers", nil)
	req.Header.Set("Origin", "https://ui.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://ui.example" {
		t.Errorf("Allow-Origin = %q, want the configured origin", got)
	}

	// A disallowed origin gets no CORS grant.
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for a non-allowed origin", got)
	}
}

// With no AllowedOrigins, CORS is off (same-origin only) — no grant headers.
func TestCORS_DisabledByDefault(t *testing.T) {
	h := Router(Options{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://ui.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty when CORS disabled", got)
	}
}
