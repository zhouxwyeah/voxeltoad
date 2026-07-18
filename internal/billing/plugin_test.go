package billing_test

import (
	"context"
	"errors"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
)

// billingDyn: alias "chat" served by two providers. Completion rates differ so
// we can assert the Pre estimate uses the MAX candidate completion rate, and
// Post uses the actual hit provider's rate. DefaultMaxTokens drives the estimate
// when the request omits max_tokens.
func billingDyn() func() *config.Dynamic {
	dyn := &config.Dynamic{
		Models: []config.Model{{
			Alias: "chat",
			Upstreams: []config.ModelUpstream{
				{Provider: "openai", UpstreamModel: "gpt-4o", DefaultMaxTokens: 100,
					Pricing: config.Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000}},
				{Provider: "claude", UpstreamModel: "c", DefaultMaxTokens: 200,
					Pricing: config.Pricing{PromptPer1M: 3_000_000, CompletionPer1M: 30_000_000}},
			},
		}},
	}
	return func() *config.Dynamic { return dyn }
}

func preCtx(tenant, alias string) *plugin.Context {
	return &plugin.Context{Ctx: context.Background(), Tenant: tenant, Request: &adapter.UnifiedRequest{Model: alias}}
}

func intp(n int) *int { return &n }

// Pre pre-debits the completion-only estimate. With max_tokens=1000 and the max
// candidate completion rate 30_000_000/1M, est = 1000/1_000_000*30_000_000 = 30000.
func TestPlugin_PreDebitsEstimate(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:rich", 100000)
	p := billing.NewPlugin(billingDyn(), q, billing.NewMemoryUsageRecorder())

	c := preCtx("rich", "chat")
	c.Request.MaxTokens = intp(1000)
	if err := runPhase(p, c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatalf("Pre should pass with ample balance; BlockedBy=%q", c.BlockedBy)
	}
	if bal := q.Balance("tenant:rich"); bal != 100000-30000 {
		t.Errorf("balance after pre-debit = %d, want %d (est 30000)", bal, 100000-30000)
	}
}

// When max_tokens is omitted, the estimate uses the max DefaultMaxTokens across
// candidates (200) × max completion rate (30_000_000/1M) = 6000.
func TestPlugin_PreEstimateUsesDefaultMaxTokens(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:rich", 100000)
	p := billing.NewPlugin(billingDyn(), q, billing.NewMemoryUsageRecorder())

	c := preCtx("rich", "chat") // no MaxTokens
	_ = runPhase(p, c, plugin.PhasePre)
	if bal := q.Balance("tenant:rich"); bal != 100000-6000 {
		t.Errorf("balance = %d, want %d (est 200*30_000_000/1_000_000)", bal, 100000-6000)
	}
}

// Insufficient balance ⇒ Pre rejects with RejectStatus 402 (not the rate
// limiter's 429), nothing debited.
func TestPlugin_PreRejects402WhenInsufficient(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:broke", 10) // far below any estimate
	p := billing.NewPlugin(billingDyn(), q, billing.NewMemoryUsageRecorder())

	c := preCtx("broke", "chat")
	c.Request.MaxTokens = intp(1000)
	_ = runPhase(p, c, plugin.PhasePre)

	if !c.Stop || c.BlockedBy != "billing" {
		t.Fatalf("expected Pre to block; Stop=%v BlockedBy=%q", c.Stop, c.BlockedBy)
	}
	if c.RejectStatus != 402 {
		t.Errorf("RejectStatus = %d, want 402 (insufficient quota)", c.RejectStatus)
	}
	if bal := q.Balance("tenant:broke"); bal != 10 {
		t.Errorf("nothing should be debited on rejection; balance = %d, want 10", bal)
	}
}

// Store unreachable ⇒ fail closed: Pre rejects with RejectStatus 503 (ADR-0013).
func TestPlugin_PreFailsClosedOn503(t *testing.T) {
	p := billing.NewPlugin(billingDyn(), errQuotaStore{}, billing.NewMemoryUsageRecorder())

	c := preCtx("any", "chat")
	c.Request.MaxTokens = intp(10)
	if err := runPhase(p, c, plugin.PhasePre); err != nil {
		t.Fatalf("Pre should not return an error (it rejects via Context); got %v", err)
	}
	if !c.Stop || c.RejectStatus != 503 {
		t.Errorf("store outage should fail closed with 503; Stop=%v RejectStatus=%d", c.Stop, c.RejectStatus)
	}
}

// Post settles the difference: est was 30000 (max_tokens 1000 × 30_000_000/1M), actual
// at the hit provider (openai: 1000 prompt @5_000_000 + 1000 completion @15_000_000 =
// 20000). delta = 30000 - 20000 = 10000 refunded.
func TestPlugin_PostSettlesDifference(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:rich", 100000)
	rec := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(billingDyn(), q, rec)

	c := preCtx("rich", "chat")
	c.Request.MaxTokens = intp(1000)
	_ = runPhase(p, c, plugin.PhasePre) // pre-debit 30000 → 70000
	if bal := q.Balance("tenant:rich"); bal != 70000 {
		t.Fatalf("after pre-debit balance = %d, want 70000", bal)
	}

	c.Provider = "openai"
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000}}
	if err := runPhase(p, c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	// refund 10000 → 80000.
	if bal := q.Balance("tenant:rich"); bal != 80000 {
		t.Errorf("balance after settle = %d, want 80000 (refund 10000)", bal)
	}
	recs := rec.Records()
	if len(recs) != 1 || recs[0].Provider != "openai" || recs[0].Cost != 20000 {
		t.Errorf("records = %+v, want one openai record cost 20000", recs)
	}
}

// Total failure / no usage ⇒ actual=0 ⇒ FULL refund of the reservation (never a
// no-op, which would leak the pre-debit). ADR-0013/0016.
func TestPlugin_PostFullRefundOnNoUsage(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:rich", 100000)
	rec := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(billingDyn(), q, rec)

	c := preCtx("rich", "chat")
	c.Request.MaxTokens = intp(1000)
	_ = runPhase(p, c, plugin.PhasePre) // pre-debit 30000 → 70000

	c.Provider = "openai"
	c.Response = &adapter.UnifiedResponse{} // no usage (upstream failed / empty stream)
	_ = runPhase(p, c, plugin.PhasePost)

	if bal := q.Balance("tenant:rich"); bal != 100000 {
		t.Errorf("no usage must fully refund the reservation; balance = %d, want 100000", bal)
	}
	if len(rec.Records()) != 0 {
		t.Error("no usage → no usage record")
	}
}

// B1 fix: settle MUST survive request-context cancellation. A mid-stream client
// disconnect cancels c.Ctx, but the refund must still land (quota is money).
// settleContext uses context.WithoutCancel so the detached ctx runs to
// completion. This was previously only guarded by e2e; this unit test pins it.
func TestPlugin_PostSettle_SurvivesContextCancellation(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:rich", 100000)
	rec := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(billingDyn(), q, rec)

	c := preCtx("rich", "chat")
	c.Request.MaxTokens = intp(1000)
	_ = runPhase(p, c, plugin.PhasePre) // pre-debit 30000 → 70000

	// Simulate a client disconnect: cancel the request context BEFORE Post runs.
	reqCtx, cancelReq := context.WithCancel(context.Background())
	c.Ctx = reqCtx
	cancelReq() // now c.Ctx is cancelled

	c.Provider = "openai"
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000}}
	// Post must NOT error despite the cancelled ctx (settle detaches cancellation).
	if err := runPhase(p, c, plugin.PhasePost); err != nil {
		t.Fatalf("Post should survive ctx cancellation; got %v", err)
	}
	// Refund 10000 (30000 reserved − 20000 actual) must still land → 80000.
	if bal := q.Balance("tenant:rich"); bal != 80000 {
		t.Errorf("settle after disconnect must still refund; balance = %d, want 80000", bal)
	}
	if recs := rec.Records(); len(recs) != 1 || recs[0].Cost != 20000 {
		t.Errorf("usage record must still land after disconnect; got %+v", recs)
	}
}

// When reserved == actual (delta 0), Settle is skipped (no-op) but the usage
// record is still written. Guards the delta != 0 early-exit in settle().
func TestPlugin_PostSettle_DeltaZeroNoOp(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	// Set balance so pre-debit + settle leaves it unchanged: we craft actual to
	// equal reserved. est = max_tokens(1000) × max completion rate(30_000_000/1M) = 30000.
	q.SetBalance("tenant:rich", 100000)
	trackQ := &trackingQuotaStore{inner: q}
	rec := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(billingDyn(), trackQ, rec)

	c := preCtx("rich", "chat")
	c.Request.MaxTokens = intp(1000)
	_ = runPhase(p, c, plugin.PhasePre) // pre-debit 30000 → 70000

	// Hit claude: actual must equal 30000 to make delta 0.
	// claude prompt 3_000_000/1M, completion 30_000_000/1M.
	// We need prompt*3M + completion*30M = 30000*1M = 30_000_000_000 micros.
	// 5000 prompt × 3_000_000 = 15_000_000_000; need 15_000_000_000 more from completion.
	// 15_000_000_000 / 30_000_000 = 500 completion tokens. Total actual = 30000.
	c.Provider = "claude"
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 5000, CompletionTokens: 500}}
	if err := runPhase(p, c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	// Balance unchanged: reserved==actual so Settle was a no-op → 70000.
	if bal := q.Balance("tenant:rich"); bal != 70000 {
		t.Errorf("delta=0 should not change balance; got %d, want 70000", bal)
	}
	if trackQ.settleCalls != 0 {
		t.Errorf("Settle should not be called when delta=0; got %d calls", trackQ.settleCalls)
	}
	// Usage record still written.
	if recs := rec.Records(); len(recs) != 1 || recs[0].Cost != 30000 {
		t.Errorf("usage record should still be written; got %+v", recs)
	}
}

// When the hit provider's pricing can't be resolved (model not found in dyn
// config), actual stays 0 → full refund of the reservation. Guards the
// pricingFor ok=false path.
func TestPlugin_PostSettle_PricingNotFound(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:rich", 100000)
	rec := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(billingDyn(), q, rec)

	c := preCtx("rich", "chat")
	c.Request.MaxTokens = intp(1000)
	_ = runPhase(p, c, plugin.PhasePre) // pre-debit 30000 → 70000

	// Provider that doesn't match any upstream → pricingFor returns ok=false.
	c.Provider = "ghost-provider"
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000}}
	_ = runPhase(p, c, plugin.PhasePost)

	// actual=0 (no pricing) → full refund of 30000 → 100000.
	if bal := q.Balance("tenant:rich"); bal != 100000 {
		t.Errorf("unresolved pricing must fully refund; balance = %d, want 100000", bal)
	}
	// Usage record IS written (usage present), but with cost 0.
	recs := rec.Records()
	if len(recs) != 1 || recs[0].Cost != 0 {
		t.Errorf("usage record with cost 0 expected; got %+v", recs)
	}
}

// Multi-scope settle: a caller with tenant+group+key scopes debits across all
// three, and Post settles the delta back across all three. Guards the
// multi-scope path (previously only single-scope was tested).
func TestPlugin_MultiScopeSettle(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	// All three scopes configured; TryDebit is all-or-nothing.
	q.SetBalance("tenant:acme", 100000)
	q.SetBalance("group:team-a", 100000)
	q.SetBalance("key:k1", 100000)
	rec := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(billingDyn(), q, rec)

	c := preCtx("acme", "chat")
	c.Group = "team-a"
	c.APIKeyID = "k1"
	c.Request.MaxTokens = intp(1000)
	if err := runPhase(p, c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatalf("Pre should pass with all scopes funded")
	}
	// Each scope debited 30000.
	if bal := q.Balance("tenant:acme"); bal != 70000 {
		t.Errorf("tenant scope balance = %d, want 70000", bal)
	}
	if bal := q.Balance("group:team-a"); bal != 70000 {
		t.Errorf("group scope balance = %d, want 70000", bal)
	}
	if bal := q.Balance("key:k1"); bal != 70000 {
		t.Errorf("key scope balance = %d, want 70000", bal)
	}

	c.Provider = "openai"
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000}}
	if err := runPhase(p, c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	// delta = 30000 − 20000 = 10000 refunded to EACH scope → 80000 each.
	for scope, want := range map[string]int64{
		"tenant:acme":  80000,
		"group:team-a": 80000,
		"key:k1":       80000,
	} {
		if bal := q.Balance(scope); bal != want {
			t.Errorf("%s balance = %d, want %d (multi-scope settle)", scope, bal, want)
		}
	}
}

// trackingQuotaStore wraps a QuotaStore to count Settle calls (for the
// delta-zero no-op assertion).
type trackingQuotaStore struct {
	inner       billing.QuotaStore
	settleCalls int
}

func (t *trackingQuotaStore) TryDebit(ctx context.Context, scopes []string, amount int64) (bool, error) {
	return t.inner.TryDebit(ctx, scopes, amount)
}
func (t *trackingQuotaStore) Settle(ctx context.Context, scopes []string, delta int64) error {
	t.settleCalls++
	return t.inner.Settle(ctx, scopes, delta)
}

// errQuotaStore simulates an unreachable quota store (fail-closed path).
type errQuotaStore struct{}

func (errQuotaStore) TryDebit(context.Context, []string, int64) (bool, error) {
	return false, errors.New("quota store unreachable")
}
func (errQuotaStore) Settle(context.Context, []string, int64) error {
	return errors.New("quota store unreachable")
}

// runPhase invokes the plugin only if it matches the phase (mirrors Chain.Run
// filtering), so a single plugin can be exercised per phase.
func runPhase(p plugin.Plugin, c *plugin.Context, ph plugin.Phase) error {
	for _, x := range p.Phases() {
		if x == ph {
			return p.Execute(c, ph)
		}
	}
	return nil
}
