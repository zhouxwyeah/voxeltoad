package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voxeltoad/internal/proxy"
)

// The router resolves its dispatcher via WithDispatcherProvider on every
// request, so swapping the provider's return value hot-swaps routing without
// rebuilding the router (the config hot-reload model).
func TestRouter_DispatcherProviderHotSwap(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	var holder atomic.Pointer[proxy.Dispatcher]
	h := proxy.Router(nil, proxy.WithDispatcherProvider(func() *proxy.Dispatcher {
		return holder.Load()
	}))

	// Initially nil → 501.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when provider returns nil", rr.Code)
	}

	// Swap in a working dispatcher → 200, no router rebuild.
	holder.Store(newDispatcherFor(t, up.URL))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after hot-swap; body=%s", rr.Code, rr.Body.String())
	}
}
