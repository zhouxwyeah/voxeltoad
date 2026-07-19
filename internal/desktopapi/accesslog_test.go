package desktopapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn while capturing everything observability.Logger()
// writes to os.Stdout (it builds a fresh slog handler over os.Stdout per call).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// TestWithAccessLog_LogsRequest: a normal read-API call must produce one
// structured line with method, path, query, status and duration.
func TestWithAccessLog_LogsRequest(t *testing.T) {
	h := WithAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	logged := captureStdout(t, func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/overview?from=2026-07-19T00:00:00Z", nil))
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", rr.Code)
		}
	})

	for _, want := range []string{
		`"level":"INFO"`,
		`"msg":"GET /api/v1/overview"`,
		`"status":201`,
		`"query":"from=2026-07-19T00:00:00Z"`,
		"duration_ms",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("access log missing %q; got:\n%s", want, logged)
		}
	}
}

// TestWithAccessLog_SkipsPollingEndpoints: /api/v1/logs (3s UI poll) and
// /api/v1/health (readiness probes) must stay silent or they flood the ring.
func TestWithAccessLog_SkipsPollingEndpoints(t *testing.T) {
	h := WithAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	logged := captureStdout(t, func() {
		for _, p := range []string{"/api/v1/logs?tail=500", "/api/v1/health"} {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		}
	})

	if strings.Contains(logged, "/api/v1/logs") || strings.Contains(logged, "/api/v1/health") {
		t.Errorf("polling endpoints must not be logged; got:\n%s", logged)
	}
}

// TestWithAccessLog_WarnOn4xx: client errors (e.g. a stale-UI 404) must stand
// out at WARN level.
func TestWithAccessLog_WarnOn4xx(t *testing.T) {
	h := WithAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))

	logged := captureStdout(t, func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rr.Code)
		}
	})

	if !strings.Contains(logged, `"level":"WARN"`) || !strings.Contains(logged, `"status":404`) {
		t.Errorf("404 must log at WARN with status; got:\n%s", logged)
	}
}
