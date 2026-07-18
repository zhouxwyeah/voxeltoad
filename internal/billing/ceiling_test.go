package billing_test

import (
	"testing"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
)

// dynNoDefault is a model whose candidates set NO DefaultMaxTokens, so without a
// global ceiling the completion-only estimate would be 0 (the boundary bug).
func dynNoDefault() func() *config.Dynamic {
	dyn := &config.Dynamic{
		Models: []config.Model{{
			Alias: "chat",
			Upstreams: []config.ModelUpstream{
				{Provider: "openai", UpstreamModel: "gpt-4o",
					Pricing: config.Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000}},
			},
		}},
	}
	return func() *config.Dynamic { return dyn }
}

// With no max_tokens on the request AND no DefaultMaxTokens in config, a global
// ceiling drives a non-zero reservation so quota pre-debit still runs. Without
// it, est would be 0 and the debit a no-op (bypass).
func TestPlugin_MaxTokensCeilingReservesWhenNoTokensGiven(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:t", 1_000_000)
	p := billing.NewPlugin(dynNoDefault(), q, billing.NewMemoryUsageRecorder(),
		billing.WithMaxTokensCeiling(512))

	c := preCtx("t", "chat") // no MaxTokens
	if err := runPhase(p, c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	// est = ceiling(512) * maxCompletionRate(15_000_000) / 1_000_000 = 7680.
	if bal := q.Balance("tenant:t"); bal != 1_000_000-7680 {
		t.Errorf("balance = %d, want %d (ceiling-driven reservation 7680)", bal, 1_000_000-7680)
	}
	if c.Stop {
		t.Error("should not block a funded tenant")
	}
}

// The boundary bug closed: a tenant with too little balance is REJECTED (402)
// even when the request omits max_tokens and config has no DefaultMaxTokens —
// previously the estimate was 0 and the request slipped through.
func TestPlugin_MaxTokensCeilingRejectsBrokeTenant(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:broke", 10) // far below the ceiling-driven estimate
	p := billing.NewPlugin(dynNoDefault(), q, billing.NewMemoryUsageRecorder(),
		billing.WithMaxTokensCeiling(512))

	c := preCtx("broke", "chat") // no MaxTokens
	_ = runPhase(p, c, plugin.PhasePre)

	if !c.Stop || c.RejectStatus != 402 {
		t.Errorf("expected 402 rejection for broke tenant; Stop=%v RejectStatus=%d", c.Stop, c.RejectStatus)
	}
	if bal := q.Balance("tenant:broke"); bal != 10 {
		t.Errorf("nothing should be debited on rejection; balance = %d", bal)
	}
}

// Request max_tokens still wins over the ceiling when present.
func TestPlugin_ExplicitMaxTokensOverridesCeiling(t *testing.T) {
	q := billing.NewMemoryQuotaStore()
	q.SetBalance("tenant:t", 1_000_000)
	p := billing.NewPlugin(dynNoDefault(), q, billing.NewMemoryUsageRecorder(),
		billing.WithMaxTokensCeiling(512))

	c := preCtx("t", "chat")
	c.Request.MaxTokens = intp(100) // explicit, below the ceiling
	_ = runPhase(p, c, plugin.PhasePre)

	// est = 100 * 15_000_000 / 1_000_000 = 1500 (uses request max_tokens, not ceiling).
	if bal := q.Balance("tenant:t"); bal != 1_000_000-1500 {
		t.Errorf("balance = %d, want %d (explicit max_tokens 100)", bal, 1_000_000-1500)
	}
}
