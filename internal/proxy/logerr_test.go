package proxy_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"voxeltoad/internal/proxy"
)

// captureStdout runs fn while capturing everything observability.Logger() writes
// to os.Stdout (it builds a fresh slog handler over os.Stdout each call).
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

// pickLine returns the newline-delimited line in logged that contains substr.
func pickLine(logged, substr string) string {
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}

// TestAccessLog_SelfExplanatory: the single INFO access line for a chat request
// must carry request_id, model, and (on failure) error_type — so it is
// diagnosable without cross-referencing the error line (C: self-explanatory
// access log).
func TestAccessLog_SelfExplanatory(t *testing.T) {
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

	// The access (INFO) line is the one whose msg is the request path.
	line := pickLine(logged, `msg":"POST /v1/chat/completions"`)
	if line == "" {
		t.Fatalf("access log line not found; got:\n%s", logged)
	}
	for _, want := range []string{"request_id", "model", "gpt-4o", "error_type", "upstream_error"} {
		if !strings.Contains(line, want) {
			t.Errorf("access line missing %q; got:\n%s", want, line)
		}
	}
}

// TestAccessLog_SuccessCarriesRequestIDAndModel: even on success the access line
// must carry request_id and model for correlation/grepping.
func TestAccessLog_SuccessCarriesRequestIDAndModel(t *testing.T) {
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
	for _, want := range []string{"request_id", "model", "gpt-4o"} {
		if !strings.Contains(line, want) {
			t.Errorf("access line missing %q; got:\n%s", want, line)
		}
	}
}

// TestForwardFailure_LogsErrorLine: a 502 must also emit a server-side
// error-level log line carrying the underlying cause (request_id, provider,
// error_type, truncated error) — not just the client response body. This is the
// observability gap fixed by logForwardFailure: before it, 5xx were only an
// INFO access-log line with no reason.
func TestForwardFailure_LogsErrorLine(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream boom"))
	})
	h := proxy.Router(newDispatcherFor(t, up), proxy.WithAccessLog())

	// observability.Logger() writes to os.Stdout; capture it for the request.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))

	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	logged := buf.String()

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	for _, want := range []string{
		"upstream request failed",
		"error_type",
		"upstream_error",
		"upstream returned 500: upstream boom",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("error log missing %q; got:\n%s", want, logged)
		}
	}
}

// TestForwardFailure_TruncatesLongError: a very large upstream body must be
// bounded in the log line (truncate cap), so a malicious/oversized upstream
// response can't blow up the log line.
func TestForwardFailure_TruncatesLongError(t *testing.T) {
	big := strings.Repeat("X", 5000)
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(big))
	})
	h := proxy.Router(newDispatcherFor(t, up), proxy.WithAccessLog())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))

	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	logged := buf.String()

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "upstream request failed") {
			// The "error" field must be truncated well below 5000.
			if i := strings.Index(line, `"error":"`); i >= 0 {
				rest := line[i+len(`"error":"`):]
				end := strings.Index(rest, `"`)
				// truncate caps at 256 bytes + a 3-byte ellipsis ("…") = 259.
				if end > 300 {
					t.Errorf("error field not truncated: %d bytes", end)
				}
				if !strings.Contains(rest[:end], "…") {
					t.Errorf("error field missing truncation ellipsis")
				}
			}
		}
	}
}
