package billing_test

import (
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
)

// Cost is in int64 micro-units (ADR-0013): pricing rates are micro-units per
// million tokens, cost = round_half_up( tokens/1_000_000 * rate ).
func TestCost(t *testing.T) {
	// $5 / $15 per million tokens, expressed in micro-units.
	pricing := config.Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000, Currency: "USD"}

	tests := []struct {
		name  string
		usage *adapter.Usage
		want  int64
	}{
		{
			name:  "prompt and completion at different rates",
			usage: &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000},
			want:  20000, // 5000 + 15000
		},
		{
			name:  "sub-1k tokens prorated",
			usage: &adapter.Usage{PromptTokens: 500, CompletionTokens: 200},
			want:  5500, // 2500 + 3000
		},
		{
			name:  "nil usage is zero cost",
			usage: nil,
			want:  0,
		},
		{
			name:  "zero usage is zero cost",
			usage: &adapter.Usage{},
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := billing.Cost(tt.usage, pricing)
			if got != tt.want {
				t.Errorf("Cost = %d, want %d", got, tt.want)
			}
		})
	}
}

// Round-half-up is applied once at the end (ADR-0013, deterministic).
func TestCostRoundsHalfUp(t *testing.T) {
	// 1 completion token at 1_500_000 micro/1M = 1.5 micro → rounds up to 2.
	pricing := config.Pricing{CompletionPer1M: 1_500_000}
	got := billing.Cost(&adapter.Usage{CompletionTokens: 1}, pricing)
	if got != 2 {
		t.Errorf("Cost = %d, want 2 (1.5 rounded half up)", got)
	}
}

// TestCostRoundingBoundaries pins the round-half-up behavior at both sides of
// the 0.5 micro boundary, so a future refactor doesn't accidentally flip to
// truncation or round-half-down.
func TestCostRoundingBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		usage   *adapter.Usage
		pricing config.Pricing
		want    int64
	}{
		{
			// 1 token × 1_499_999/1M = 1.499999 → rounds DOWN to 1.
			name:    "just below half rounds down",
			usage:   &adapter.Usage{CompletionTokens: 1},
			pricing: config.Pricing{CompletionPer1M: 1_499_999},
			want:    1,
		},
		{
			// 1 token × 1_500_001/1M = 1.500001 → rounds UP to 2.
			name:    "just above half rounds up",
			usage:   &adapter.Usage{CompletionTokens: 1},
			pricing: config.Pricing{CompletionPer1M: 1_500_001},
			want:    2,
		},
		{
			// Exactly 0.5 micro: 1 token × 500_000/1M = 0.5 → rounds UP to 1.
			name:    "exact half rounds up",
			usage:   &adapter.Usage{CompletionTokens: 1},
			pricing: config.Pricing{CompletionPer1M: 500_000},
			want:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := billing.Cost(tt.usage, tt.pricing); got != tt.want {
				t.Errorf("Cost = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCostNegativeTokens documents the behavior of negative token counts /
// negative rates. These are not valid inputs in production (upstream usage is
// non-negative), but the test pins the current integer-arithmetic behavior so
// an unexpected negative doesn't silently produce a surprising result without
// someone noticing. If guard logic is added later, update this test.
func TestCostNegativeTokens(t *testing.T) {
	// Negative prompt tokens: micros goes negative; Go's integer division
	// truncates toward zero, and the +500_000 bias shifts it. We document the
	// actual output rather than asserting "should be 0".
	pricing := config.Pricing{PromptPer1M: 5_000_000}
	got := billing.Cost(&adapter.Usage{PromptTokens: -1000}, pricing)
	// micros = -1000 * 5_000_000 = -5_000_000_000; (-5_000_000_000 + 500_000) / 1_000_000
	// = -4999500000 / 1_000_000 = -4999 (truncation toward zero).
	if got != -4999 {
		t.Errorf("Cost(-1000 prompt) = %d; if this changed, update the guard", got)
	}
}

// TestCost_CacheHitMultiplier covers the prompt-cache discount path: the cached
// portion of prompt tokens is billed at PromptPer1M × multiplier, the rest at
// full PromptPer1M. Completion is unaffected. Cases exercise:
//   - multiplier unconfigured (0) → full price (legacy-safe)
//   - multiplier 1.0 → no discount (identical to no caching)
//   - multiplier 0.5 → half off on cached tokens
//   - multiplier tiny → near-free cache reads
//   - cached > prompt clamp (defensive against upstream anomalies)
//   - overflow boundary (large token counts × large rates stay in int64 range)
func TestCost_CacheHitMultiplier(t *testing.T) {
	tests := []struct {
		name    string
		usage   *adapter.Usage
		pricing config.Pricing
		want    int64
	}{
		{
			name:    "multiplier zero = full price (legacy-safe)",
			usage:   &adapter.Usage{PromptTokens: 1000, CompletionTokens: 0, CachedPromptTokens: 400},
			pricing: config.Pricing{PromptPer1M: 5_000_000}, // CacheHitMultiplier unset
			want:    5000,                                   // 1000 × 5_000_000 / 1M = 5000 (no discount applied)
		},
		{
			name:    "multiplier 1.0 = no discount",
			usage:   &adapter.Usage{PromptTokens: 1000, CompletionTokens: 0, CachedPromptTokens: 400},
			pricing: config.Pricing{PromptPer1M: 5_000_000, CacheHitMultiplier: 1_000_000},
			want:    5000, // identical to no-cache
		},
		{
			name:    "multiplier 0.5 = half off cached portion",
			usage:   &adapter.Usage{PromptTokens: 1000, CompletionTokens: 0, CachedPromptTokens: 400},
			pricing: config.Pricing{PromptPer1M: 5_000_000, CacheHitMultiplier: 500_000},
			// non-cached 600 × 5 = 3000; cached 400 × (5 × 0.5 = 2.5) = 1000; total 4000.
			want: 4000,
		},
		{
			name:    "multiplier 0.1 = 10% of cached portion",
			usage:   &adapter.Usage{PromptTokens: 1000, CompletionTokens: 0, CachedPromptTokens: 400},
			pricing: config.Pricing{PromptPer1M: 5_000_000, CacheHitMultiplier: 100_000},
			// non-cached 600 × 5 = 3000; cached 400 × (5 × 0.1 = 0.5) = 200; total 3200.
			want: 3200,
		},
		{
			name:    "multiplier ≈0 = near-free cache reads (multiplier=1 ≈ 0.0001%)",
			usage:   &adapter.Usage{PromptTokens: 1000, CompletionTokens: 0, CachedPromptTokens: 400},
			pricing: config.Pricing{PromptPer1M: 5_000_000, CacheHitMultiplier: 1}, // ≈0.0001%
			// non-cached 600 × 5 = 3000; cached 400 × ~0 = ~0; total ~3000.
			want: 3000,
		},
		{
			name:    "cached clamped to prompt when upstream anomaly",
			usage:   &adapter.Usage{PromptTokens: 100, CachedPromptTokens: 500},
			pricing: config.Pricing{PromptPer1M: 5_000_000, CacheHitMultiplier: 500_000},
			// cached clamped to 100; non-cached 0; cached 100 × 2.5 = 250.
			want: 250,
		},
		{
			name:    "mixed prompt + completion with cache discount",
			usage:   &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000, CachedPromptTokens: 400},
			pricing: config.Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000, CacheHitMultiplier: 500_000},
			// prompt: 600×5 + 400×2.5 = 3000 + 1000 = 4000; completion: 1000×15 = 15000.
			want: 19000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := billing.Cost(tt.usage, tt.pricing)
			if got != tt.want {
				t.Errorf("Cost = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCost_NoOverflow ensures the two-stage reduction keeps intermediate
// products within int64 even for extreme inputs. A naive formula
// (cached × PromptPer1M × mul) would overflow here.
func TestCost_NoOverflow(t *testing.T) {
	// 1M tokens, $10M/1M rate, 100% multiplier — naive formula hits 1e19.
	usage := &adapter.Usage{PromptTokens: 1_000_000, CachedPromptTokens: 1_000_000}
	pricing := config.Pricing{PromptPer1M: 10_000_000, CacheHitMultiplier: 1_000_000}
	got := billing.Cost(usage, pricing)
	// All cached at full price: 1M tokens × 10M micro/1M = 10M micro ($10).
	if got != 10_000_000 {
		t.Errorf("Cost(extreme) = %d, want 10_000_000 (no overflow)", got)
	}
}

// TestFullCost verifies FullCost ignores the cache multiplier entirely and
// bills all prompt tokens at full rate. Used to derive CacheDiscountMicros.
func TestFullCost(t *testing.T) {
	usage := &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000, CachedPromptTokens: 400}
	pricing := config.Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000, CacheHitMultiplier: 500_000}
	// FullCost ignores multiplier: 1000×5 + 1000×15 = 5000 + 15000 = 20000.
	got := billing.FullCost(usage, pricing)
	if got != 20000 {
		t.Errorf("FullCost = %d, want 20000", got)
	}

	// Nil usage → 0.
	if billing.FullCost(nil, pricing) != 0 {
		t.Error("FullCost(nil) should be 0")
	}

	// Discount = FullCost - Cost (with multiplier 0.5 on 400 cached):
	// Cost = 19000 (from TestCost_CacheHitMultiplier), discount = 20000 - 19000 = 1000.
	cost := billing.Cost(usage, pricing)
	if discount := got - cost; discount != 1000 {
		t.Errorf("discount = FullCost(%d) - Cost(%d) = %d, want 1000", got, cost, discount)
	}
}
