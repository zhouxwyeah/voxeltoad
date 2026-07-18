package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voxeltoad/internal/plugin"
	"voxeltoad/internal/proxy"
)

// recordingPlugin records Pre/Post invocations and can stop the chain in Pre.
type recordingPlugin struct {
	phase     plugin.Phase
	stop      bool
	rejectSt  int // optional RejectStatus to set when stopping in Pre (0 = default)
	pre       *int32
	post      *int32
	gotUsage  *int32 // total tokens seen in Post (from Context.Response)
	gotProvid *string
}

func (p *recordingPlugin) Name() string           { return "recorder" }
func (p *recordingPlugin) Phases() []plugin.Phase { return []plugin.Phase{p.phase} }
func (p *recordingPlugin) Execute(c *plugin.Context, _ plugin.Phase) error {
	if p.phase == plugin.PhasePre {
		atomic.AddInt32(p.pre, 1)
		if p.stop {
			c.Stop = true
			c.BlockedBy = p.Name()
			if p.rejectSt != 0 {
				c.RejectStatus = p.rejectSt
			}
		}
		return nil
	}
	// Post
	atomic.AddInt32(p.post, 1)
	if c.Response != nil && c.Response.Usage != nil {
		atomic.StoreInt32(p.gotUsage, int32(c.Response.Usage.TotalTokens))
	}
	if p.gotProvid != nil {
		*p.gotProvid = c.Provider
	}
	return nil
}

func TestHook_PreAndPostRunAroundForward(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody)) // usage total 2 (from dispatcher_test okBody)
	}))
	defer up.Close()

	var pre, post, usage int32
	var provider string
	chain := plugin.NewChain(
		&recordingPlugin{phase: plugin.PhasePre, pre: &pre, post: &post, gotUsage: &usage},
		&recordingPlugin{phase: plugin.PhasePost, pre: &pre, post: &post, gotUsage: &usage, gotProvid: &provider},
	)

	h := proxy.Router(newDispatcherFor(t, up.URL), proxy.WithPlugins(chain))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if atomic.LoadInt32(&pre) != 1 {
		t.Errorf("pre ran %d times, want 1", pre)
	}
	if atomic.LoadInt32(&post) != 1 {
		t.Errorf("post ran %d times, want 1", post)
	}
	if atomic.LoadInt32(&usage) != 2 {
		t.Errorf("post saw usage total %d, want 2 (from response)", usage)
	}
	if provider == "" {
		t.Error("post should see the hit provider")
	}
}

func TestHook_PreRejectionReturns429AndSkipsForward(t *testing.T) {
	upHit := int32(0)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHit, 1)
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	var pre, post int32
	chain := plugin.NewChain(
		&recordingPlugin{phase: plugin.PhasePre, stop: true, pre: &pre, post: &post, gotUsage: new(int32)},
	)
	h := proxy.Router(newDispatcherFor(t, up.URL), proxy.WithPlugins(chain))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (Pre stopped the chain)", rr.Code)
	}
	if atomic.LoadInt32(&upHit) != 0 {
		t.Errorf("upstream hit %d times, want 0 (rejected before forward)", upHit)
	}
}

// A Pre rejection carrying RejectStatus 402 (quota exhausted) maps to HTTP 402,
// distinct from the rate limiter's default 429 (ADR-0013).
func TestHook_PreRejectionRespectsRejectStatus(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	var pre, post int32
	chain := plugin.NewChain(
		&recordingPlugin{phase: plugin.PhasePre, stop: true, rejectSt: http.StatusPaymentRequired,
			pre: &pre, post: &post, gotUsage: new(int32)},
	)
	h := proxy.Router(newDispatcherFor(t, up.URL), proxy.WithPlugins(chain))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (RejectStatus honored)", rr.Code)
	}
}

func TestHook_PostRunsOnStreamingCompletion(t *testing.T) {
	streamBody := "data: {\"id\":\"s\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"s\",\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n" +
		"data: [DONE]\n\n"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(streamBody))
	}))
	defer up.Close()

	var pre, post, usage int32
	chain := plugin.NewChain(
		&recordingPlugin{phase: plugin.PhasePost, pre: &pre, post: &post, gotUsage: &usage},
	)
	h := proxy.Router(newDispatcherFor(t, up.URL), proxy.WithPlugins(chain))
	rr := httptest.NewRecorder()
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if atomic.LoadInt32(&post) != 1 {
		t.Errorf("post ran %d times on streaming, want 1", post)
	}
	// Streaming Post must see the aggregated trailing usage.
	if atomic.LoadInt32(&usage) != 4 {
		t.Errorf("streaming post saw usage %d, want 4 (trailing chunk)", usage)
	}
}

func TestHook_NoPluginsStillWorks(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()
	// No WithPlugins: handler must behave as before.
	h := proxy.Router(newDispatcherFor(t, up.URL))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 without plugins", rr.Code)
	}
}
