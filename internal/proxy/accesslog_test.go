package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/proxy"
)

// TestAccessLog_RichFieldsOnSuccess: the access line for a successful chat
// request must carry the usage fields mirrored from the telemetry accumulator —
// prompt/completion/total tokens and provider — so the desktop log viewer alone
// shows who called what and what it cost (previously only model was logged).
func TestAccessLog_RichFieldsOnSuccess(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	})
	h := proxy.Router(newDispatcherFor(t, up), proxy.WithAccessLog())

	logged := captureStdout(t, func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	line := pickLine(logged, `msg":"POST /v1/chat/completions"`)
	if line == "" {
		t.Fatalf("access log line not found; got:\n%s", logged)
	}
	// okBody carries usage {prompt:1, completion:1, total:2}.
	for _, want := range []string{
		`"level":"INFO"`,
		"request_id", "model", "gpt-4o", "provider",
		`"prompt_tokens":1`, `"completion_tokens":1`, `"total_tokens":2`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("access line missing %q; got:\n%s", want, line)
		}
	}
}

// TestAccessLog_AgentTypeMirrored: the detected agent (from User-Agent) must
// appear on the access line so per-agent greps work without the DB.
func TestAccessLog_AgentTypeMirrored(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	})
	h := proxy.Router(newDispatcherFor(t, up), proxy.WithAccessLog())

	logged := captureStdout(t, func() {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq))
		req.Header.Set("User-Agent", "claude-cli/1.0.0 (cli, native)")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	line := pickLine(logged, `msg":"POST /v1/chat/completions"`)
	if line == "" {
		t.Fatalf("access log line not found; got:\n%s", logged)
	}
	if !strings.Contains(line, `"agent_type":"claude-code"`) {
		t.Errorf("access line missing agent_type=claude-code; got:\n%s", line)
	}
}

// TestAccessLog_LevelFollowsStatus: 5xx outcomes must stand out at ERROR level
// (and 4xx at WARN) instead of drowning in a uniform INFO stream.
func TestAccessLog_LevelFollowsStatus(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	h := proxy.Router(newDispatcherFor(t, up), proxy.WithAccessLog())

	logged := captureStdout(t, func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
		if rr.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rr.Code)
		}
	})

	line := pickLine(logged, `msg":"POST /v1/chat/completions"`)
	if line == "" {
		t.Fatalf("access log line not found; got:\n%s", logged)
	}
	if !strings.Contains(line, `"level":"ERROR"`) {
		t.Errorf("502 access line not at ERROR level; got:\n%s", line)
	}
	for _, want := range []string{"error_type", "upstream_error"} {
		if !strings.Contains(line, want) {
			t.Errorf("access line missing %q; got:\n%s", want, line)
		}
	}
}
