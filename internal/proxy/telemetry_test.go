package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"voxeltoad/internal/config"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/proxy"
)

// fakeAuditRecorder captures request-audit rows for assertions.
type fakeAuditRecorder struct {
	mu   sync.Mutex
	rows []observability.RequestLog
}

func (f *fakeAuditRecorder) Record(_ context.Context, r observability.RequestLog) {
	f.mu.Lock()
	f.rows = append(f.rows, r)
	f.mu.Unlock()
}

func (f *fakeAuditRecorder) last() observability.RequestLog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[len(f.rows)-1]
}

func (f *fakeAuditRecorder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func TestTelemetry_SuccessRecordsAuditRow(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	})
	rec := &fakeAuditRecorder{}
	h := proxy.Router(newDispatcherFor(t, up), proxy.WithAuditRecorder(rec))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	if rec.count() != 1 {
		t.Fatalf("audit rows = %d, want 1", rec.count())
	}
	row := rec.last()
	if row.ModelRequested != "gpt-4o" {
		t.Errorf("model_requested = %q, want gpt-4o", row.ModelRequested)
	}
	// newDispatcherFor is a single-provider dispatcher with no model
	// preparation configured, so ModelResolved echoes the requested alias
	// (no routing-layer resolution happened) and no fallback occurred.
	if row.ModelResolved != "gpt-4o" {
		t.Errorf("model_resolved = %q, want gpt-4o (no preparer configured)", row.ModelResolved)
	}
	if row.Fallback {
		t.Error("fallback = true, want false (single candidate, first try)")
	}
	if row.ErrorType != "" {
		t.Errorf("error_type = %q, want empty on success", row.ErrorType)
	}
	if row.TotalTokens == 0 {
		t.Error("expected non-zero total_tokens recorded from upstream usage")
	}
	if row.Durationms < 0 {
		t.Error("duration should be non-negative")
	}
	if row.RequestID == "" {
		t.Error("request_id should be non-empty (chi middleware assigns one)")
	}
}

// stopPlugin rejects every request in Pre, simulating e.g. a quota/ratelimit
// block, so we can assert rejections are still audited.
type stopPlugin struct {
	name       string
	rejectCode int
}

func (p stopPlugin) Name() string           { return p.name }
func (p stopPlugin) Phases() []plugin.Phase { return []plugin.Phase{plugin.PhasePre} }
func (p stopPlugin) Execute(c *plugin.Context, _ plugin.Phase) error {
	c.Stop = true
	c.BlockedBy = p.name
	c.RejectStatus = p.rejectCode
	return nil
}

func TestTelemetry_RejectionRecordsAuditRow(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	})
	rec := &fakeAuditRecorder{}
	chain := plugin.NewChain(stopPlugin{name: "billing", rejectCode: http.StatusPaymentRequired})
	h := proxy.Router(newDispatcherFor(t, up),
		proxy.WithPlugins(chain),
		proxy.WithAuditRecorder(rec),
	)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rr.Code)
	}

	if rec.count() != 1 {
		t.Fatalf("audit rows = %d, want 1 (rejections must be audited)", rec.count())
	}
	row := rec.last()
	if row.ErrorType != "insufficient_quota" {
		t.Errorf("error_type = %q, want insufficient_quota", row.ErrorType)
	}
	if row.BlockedBy != "billing" {
		t.Errorf("blocked_by = %q, want billing", row.BlockedBy)
	}
	if row.TotalTokens != 0 {
		t.Errorf("total_tokens = %d, want 0 for a blocked request", row.TotalTokens)
	}
	if row.RequestID == "" {
		t.Error("request_id should be non-empty on rejection too (chi middleware always assigns one)")
	}
}

// TestTelemetry_FailoverRecordsModelResolvedAndFallback: when the dispatcher
// fails over across candidates before succeeding, the audit row must capture
// the actually-resolved upstream model name and fallback=true (ADR-0021 /
// design/observability.md llm.model.resolved, llm.fallback).
func TestTelemetry_FailoverRecordsModelResolvedAndFallback(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer good.Close()

	dyn := &config.Dynamic{
		Providers: []config.Provider{
			{Name: "p-bad", Adapter: "openai"},
			{Name: "p-good", Adapter: "openai"},
		},
		Models: []config.Model{{
			Alias: "gpt-4o",
			Upstreams: []config.ModelUpstream{
				{Provider: "p-bad", UpstreamModel: "gpt-4o-bad-upstream"},
				{Provider: "p-good", UpstreamModel: "gpt-4o-good-upstream"},
			},
		}},
	}
	d := proxy.NewDispatcher(
		[]config.Route{{ModelAlias: "gpt-4o", Strategy: "priority", Providers: []config.RouteProvider{{Name: "p-bad"}, {Name: "p-good"}}}},
		map[string]*proxy.Forwarder{"p-bad": fwdTo(t, bad.URL), "p-good": fwdTo(t, good.URL)},
		proxy.DispatcherConfig{FailureThreshold: 3, Cooldown: time.Minute},
	).WithModelPreparation(dyn)

	rec := &fakeAuditRecorder{}
	h := proxy.Router(d, proxy.WithAuditRecorder(rec))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientChatReq)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	if rec.count() != 1 {
		t.Fatalf("audit rows = %d, want 1", rec.count())
	}
	row := rec.last()
	if row.Provider != "p-good" {
		t.Errorf("provider = %q, want p-good (failed over)", row.Provider)
	}
	if row.ModelResolved != "gpt-4o-good-upstream" {
		t.Errorf("model_resolved = %q, want gpt-4o-good-upstream", row.ModelResolved)
	}
	if !row.Fallback {
		t.Error("fallback = false, want true (p-bad failed before p-good succeeded)")
	}
}
