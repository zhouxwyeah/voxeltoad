package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"voxeltoad/internal/proxy"
)

// staticProbe is a trivial ReadinessProbe for tests.
type staticProbe struct{ ready bool }

func (s staticProbe) Ready(_ context.Context) bool { return s.ready }

func TestReadyz_NoProbeReturns200(t *testing.T) {
	h := proxy.Router(nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no probe = parity with /healthz)", rr.Code)
	}
}

func TestReadyz_ProbeReadyReturns200(t *testing.T) {
	h := proxy.Router(nil, proxy.WithReadinessProbe(staticProbe{ready: true}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (probe ready)", rr.Code)
	}
	if rr.Body.String() != "ready" {
		t.Errorf("body = %q, want ready", rr.Body.String())
	}
}

func TestReadyz_ProbeNotReadyReturns503(t *testing.T) {
	h := proxy.Router(nil, proxy.WithReadinessProbe(staticProbe{ready: false}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (probe not ready)", rr.Code)
	}
	if rr.Body.String() != "not ready" {
		t.Errorf("body = %q, want 'not ready'", rr.Body.String())
	}
}
