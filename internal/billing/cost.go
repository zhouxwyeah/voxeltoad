// Package billing computes request cost and enforces quota (ADR-0012). Cost is
// money (not raw tokens): prompt/completion tokens at the actually-hit
// provider's per-million-token rates. Quota is a *balance* (distinct from the TPM rate
// limit), checked allow-then-debit. Persistence is behind interfaces
// (QuotaStore, UsageRecorder); the in-memory implementations here are test-only
// — production MUST use a shared, consistent store (PG/Redis), since quota is
// money and per-instance state would overspend.
package billing

import (
	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

// Cost computes the monetary cost of a request's usage at the given pricing, in
// int64 micro-units (ADR-0013). Pricing rates are micro-units per million tokens.
// The cached portion of prompt tokens (CachedPromptTokens) is billed at
// PromptPer1M × CacheHitMultiplier; the rest of the prompt and all completion
// tokens are billed at full rate. Rounding is round-half-up, applied once per
// stage to stay deterministic and avoid int64 overflow.
//
// Overflow safety (two-stage reduction):
//   - Stage 1: cachedRate = (PromptPer1M × mul + 500_000) / 1_000_000. The
//     numerator ≤ 1e7 × 1e6 = 1e13, far below int64 max (~9.2e18).
//   - Stage 2: micros = nonCached×PromptPer1M + cached×cachedRate +
//     completion×CompletionPer1M. Each term ≤ 1e6 × 1e7 = 1e13; the sum ≤ 3e13.
//   - Final round-half-up keeps the result in range.
//
// Returns 0 for nil usage. A zero CacheHitMultiplier is treated as 1_000_000
// (full price) so legacy configurations without the field behave identically.
func Cost(u *adapter.Usage, p config.Pricing) int64 {
	if u == nil {
		return 0
	}
	mul := p.CacheHitMultiplier
	if mul == 0 {
		mul = 1_000_000 // unconfigured = full price (legacy-safe)
	}
	cached := int64(u.CachedPromptTokens)
	if cached > int64(u.PromptTokens) {
		cached = int64(u.PromptTokens) // defensive clamp against upstream anomalies
	}
	if cached < 0 {
		cached = 0
	}
	nonCached := int64(u.PromptTokens) - cached

	// Stage 1: effective per-million rate for cached tokens (micro-units), round-half-up.
	cachedRate := (p.PromptPer1M*mul + 500_000) / 1_000_000

	// Stage 2: sum all three segments in micro-units, then round-half-up once.
	micros := nonCached*p.PromptPer1M + cached*cachedRate + int64(u.CompletionTokens)*p.CompletionPer1M
	return (micros + 500_000) / 1_000_000
}

// FullCost computes what Cost would be if NO cache discount applied — i.e. all
// prompt tokens (including the cached portion) at full PromptPer1M. Used by the
// billing plugin to derive CacheDiscountMicros = FullCost - Cost for operational
// reporting ("how much did cache save?"). Returns 0 for nil usage.
func FullCost(u *adapter.Usage, p config.Pricing) int64 {
	if u == nil {
		return 0
	}
	micros := int64(u.PromptTokens)*p.PromptPer1M + int64(u.CompletionTokens)*p.CompletionPer1M
	return (micros + 500_000) / 1_000_000
}
