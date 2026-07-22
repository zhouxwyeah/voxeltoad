package desktopapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/app"
	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
	"voxeltoad/test/testsupport"
)

// newPlaygroundServer wires a Server whose dispatcher points at the given
// upstream URL (config CRUD disabled; playground only needs the watcher).
func newPlaygroundServer(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	db := openTestDB(t)
	dyn := &config.Dynamic{
		Version: "test",
		Providers: []config.Provider{{
			Name: "mock", Type: "openai",
			Endpoints: []config.ProviderEndpoint{{ID: "openai", Adapter: "openai", BaseURL: upstreamURL}},
			APIKeyRef: "plain://k", Weight: 1,
		}},
		Models: []config.Model{{
			Alias:     "m1",
			Upstreams: []config.ModelUpstream{{Provider: "mock", UpstreamModel: "m1up"}},
		}},
		Routes: []config.Route{{
			ModelAlias: "m1", Strategy: "priority",
			Providers: []config.RouteProvider{{Name: "mock", Weight: 1}},
		}},
		Settings: &config.GatewaySettings{},
	}
	watcher := app.NewDispatcherWatcher(func() *config.Dynamic { return dyn }, proxy.DispatcherConfig{})
	if err := watcher.Build(); err != nil {
		t.Fatalf("dispatcher build: %v", err)
	}
	ts := httptest.NewServer(New(db, "", watcher, nil, nil).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postPlayground(t *testing.T, ts *httptest.Server, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/playground/chat", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST playground: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestPlayground_Success(t *testing.T) {
	mu := testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-x","object":"chat.completion","model":"m1up","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	})
	defer mu.Close()
	ts := newPlaygroundServer(t, mu.URL())

	code, b := postPlayground(t, ts, `{"model":"m1","prompt":"ping"}`)
	if code != 200 {
		t.Fatalf("code = %d %s, want 200", code, b)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out["content"] != "pong" {
		t.Errorf("content = %v, want pong", out["content"])
	}
	if out["provider"] != "mock" {
		t.Errorf("provider = %v, want mock", out["provider"])
	}
	if out["model_resolved"] != "m1up" {
		t.Errorf("model_resolved = %v, want m1up", out["model_resolved"])
	}
	if usage, ok := out["usage"].(map[string]any); !ok || usage["total_tokens"] != float64(5) {
		t.Errorf("usage = %v, want total_tokens 5", out["usage"])
	}
}

// TestPlayground_ReasoningFallback: a thinking model that burned its whole
// output budget on reasoning_content (empty content, finish_reason=length)
// must surface the reasoning trace + finish reason — not a bare "（空响应）" —
// so the probe result reads as "worked, but truncated", not as a failure.
func TestPlayground_ReasoningFallback(t *testing.T) {
	mu := testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-y","object":"chat.completion","model":"m1up","choices":[{"index":0,"message":{"role":"assistant","content":"","reasoning_content":"让我先思考一下这个问题……"},"finish_reason":"length"}],"usage":{"prompt_tokens":11,"completion_tokens":64,"total_tokens":75}}`))
	})
	defer mu.Close()
	ts := newPlaygroundServer(t, mu.URL())

	code, b := postPlayground(t, ts, `{"model":"m1","prompt":"ping"}`)
	if code != 200 {
		t.Fatalf("code = %d %s, want 200", code, b)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out["content"] != "" {
		t.Errorf("content = %v, want empty", out["content"])
	}
	if out["finish_reason"] != "length" {
		t.Errorf("finish_reason = %v, want length", out["finish_reason"])
	}
	if out["reasoning_content"] != "让我先思考一下这个问题……" {
		t.Errorf("reasoning_content = %v, want the upstream trace", out["reasoning_content"])
	}
}

func TestPlayground_UpstreamErrorVerbatim(t *testing.T) {
	mu := testsupport.NewMockUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	})
	defer mu.Close()
	ts := newPlaygroundServer(t, mu.URL())

	code, b := postPlayground(t, ts, `{"model":"m1","prompt":"ping"}`)
	if code != 502 {
		t.Fatalf("code = %d, want 502", code)
	}
	if !strings.Contains(string(b), "invalid api key") {
		t.Errorf("body should carry the upstream error verbatim: %s", b)
	}
}

func TestPlayground_Validation(t *testing.T) {
	ts := newPlaygroundServer(t, "http://127.0.0.1:1")
	if code, _ := postPlayground(t, ts, `{"model":"","prompt":"x"}`); code != 400 {
		t.Errorf("empty model: code = %d, want 400", code)
	}
	if code, _ := postPlayground(t, ts, `{"model":"m1","prompt":"  "}`); code != 400 {
		t.Errorf("blank prompt: code = %d, want 400", code)
	}
}

func TestPlayground_NoDispatcher(t *testing.T) {
	db := openTestDB(t)
	ts := httptest.NewServer(New(db, "", nil, nil, nil).Handler())
	defer ts.Close()
	if code, _ := postPlayground(t, ts, `{"model":"m1","prompt":"x"}`); code != 503 {
		t.Errorf("code = %d, want 503 without a dispatcher", code)
	}
}
