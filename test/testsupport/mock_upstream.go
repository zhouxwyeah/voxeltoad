package testsupport

import (
	"net/http"
	"net/http/httptest"
)

// MockUpstream is a controllable fake upstream provider for E2E tests. It can
// serve non-streaming and streaming (SSE) responses and inject errors and usage
// values. The response-shaping logic is filled in as features land; this
// provides the server lifecycle and a pluggable handler.
//
// See design/e2e.md ("Mock 上游供应商").
type MockUpstream struct {
	srv     *httptest.Server
	handler http.HandlerFunc
}

// NewMockUpstream starts a mock upstream. If handler is nil, a default handler
// returns 501 so unconfigured paths fail loudly.
func NewMockUpstream(handler http.HandlerFunc) *MockUpstream {
	m := &MockUpstream{handler: handler}
	if m.handler == nil {
		m.handler = func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "mock upstream: handler not configured", http.StatusNotImplemented)
		}
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.handler(w, r)
	}))
	return m
}

// URL returns the base URL of the running mock upstream.
func (m *MockUpstream) URL() string { return m.srv.URL }

// SetHandler swaps the active handler, allowing per-test response shaping.
func (m *MockUpstream) SetHandler(h http.HandlerFunc) { m.handler = h }

// Close shuts down the mock upstream.
func (m *MockUpstream) Close() { m.srv.Close() }
