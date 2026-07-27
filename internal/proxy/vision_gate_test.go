package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

// visionTestDispatcher builds a Dispatcher serving one provider/model/route,
// optionally declaring the "vision" capability. Returns the router handler and
// a hit counter so a test can assert whether the upstream was reached.
func visionTestDispatcher(t *testing.T, modelCapabilities []string) (http.Handler, *int) {
	t.Helper()
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(okBody))
	}))
	t.Cleanup(up.Close)

	dyn := &config.Dynamic{
		Providers: []config.Provider{{
			Name: "p", Type: "openai",
			Endpoints: []config.ProviderEndpoint{{ID: "openai", Adapter: "openai", BaseURL: up.URL}},
			APIKeyRef: "plain://k",
			Timeouts:  config.ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 2 * time.Second, Overall: 5 * time.Second},
		}},
		Models: []config.Model{{
			Alias:        "chat",
			Capabilities: modelCapabilities,
			Upstreams:    []config.ModelUpstream{{Provider: "p", UpstreamModel: "gpt-4o"}},
		}},
		Routes: []config.Route{{
			ModelAlias: "chat",
			Providers:  []config.RouteProvider{{Name: "p"}},
			Strategy:   "priority",
		}},
	}

	disp, err := proxy.BuildDispatcher(dyn, proxy.DispatcherConfig{})
	if err != nil {
		t.Fatalf("BuildDispatcher: %v", err)
	}
	h := proxy.Router(disp)
	return h, &hits
}

// An image_url request against a text-only model (no vision capability) is
// rejected with a clear 400 unsupported_content before reaching the upstream.
func TestVisionGate_RejectsImageForTextOnlyModel(t *testing.T) {
	h, hits := visionTestDispatcher(t, nil) // no vision capability

	body := `{"model":"chat","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,xxx"}}]}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unsupported_content") {
		t.Errorf("body = %q, want error type unsupported_content", rr.Body.String())
	}
	if *hits != 0 {
		t.Errorf("upstream hits = %d, want 0 (must not forward to text-only upstream)", *hits)
	}
}

// An image_url request against a vision-capable model is forwarded to the
// upstream (the gate does not block it).
func TestVisionGate_AllowsImageForVisionModel(t *testing.T) {
	h, hits := visionTestDispatcher(t, []string{"vision"})

	body := `{"model":"chat","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,xxx"}}]}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (vision model should accept image); body=%s", rr.Code, rr.Body.String())
	}
	if *hits != 1 {
		t.Errorf("upstream hits = %d, want 1", *hits)
	}
}

// A plain-text request is unaffected by the vision gate regardless of
// capability declaration.
func TestVisionGate_PlainTextUnaffected(t *testing.T) {
	h, hits := visionTestDispatcher(t, nil) // text-only model

	body := `{"model":"chat","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (plain text must pass); body=%s", rr.Code, rr.Body.String())
	}
	if *hits != 1 {
		t.Errorf("upstream hits = %d, want 1", *hits)
	}
}
