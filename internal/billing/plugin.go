package billing

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
)

// settleTimeout bounds the Post-phase reconciliation (Settle + usage record) so
// a client disconnect — which cancels the request context — can never abort the
// refund. Without this, a cancelled ctx would fail the Settle UPDATE and leak
// the pre-debit reservation (B1 / ADR-0013).
const settleTimeout = 10 * time.Second

// Plugin enforces quota (Pre) and bills usage (Post) — ADR-0012/0013/0016. In
// Pre it pre-debits a conservative completion-only estimate across the caller's
// scopes (rejecting with 402 if insufficient, 503 if the store is unreachable —
// fail-closed). In Post it computes the exact cost at the hit provider's
// pricing, settles the est−actual difference (always — full refund when no
// usage), and records the usage for audit.
type Plugin struct {
	dyn        func() *config.Dynamic
	quota      QuotaStore
	recorder   UsageRecorder
	accounting AccountingStore
	// maxTokensCeiling bounds the completion-token estimate when neither the
	// request's max_tokens nor any candidate's DefaultMaxTokens is set, so the
	// quota pre-debit is never zero (which would silently bypass the quota check).
	maxTokensCeiling int64
}

// defaultMaxTokensCeiling is the fallback completion-token ceiling used for the
// quota reservation when nothing else bounds it (ADR-0013). Chosen to comfortably
// exceed typical completions so the reservation never under-covers; the exact
// cost is settled at Post regardless, so this only affects the transient
// reservation size, not the final charge.
const defaultMaxTokensCeiling = 4096

// Option configures the billing Plugin.
type Option func(*Plugin)

// WithMaxTokensCeiling sets the fallback completion-token ceiling used to size
// the quota reservation when a request omits max_tokens and config sets no
// DefaultMaxTokens. A value <= 0 keeps the default (defaultMaxTokensCeiling).
func WithMaxTokensCeiling(ceiling int64) Option {
	return func(p *Plugin) {
		if ceiling > 0 {
			p.maxTokensCeiling = ceiling
		}
	}
}

// WithAccounting installs the durable coordinator for enterprise requests.
// It replaces, rather than supplements, the legacy quota debit/settle path.
func WithAccounting(store AccountingStore) Option {
	return func(p *Plugin) { p.accounting = store }
}

// NewPlugin builds the billing/quota plugin over a quota store and usage
// recorder. dyn returns the current dynamic config (resolved per request) so
// pricing tracks live config swaps; it must not return nil.
func NewPlugin(dyn func() *config.Dynamic, quota QuotaStore, recorder UsageRecorder, opts ...Option) *Plugin {
	p := &Plugin{dyn: dyn, quota: quota, recorder: recorder, maxTokensCeiling: defaultMaxTokensCeiling}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name identifies the plugin.
func (p *Plugin) Name() string { return "billing" }

// Phases: quota pre-debit (Pre) + billing/settle (Post).
func (p *Plugin) Phases() []plugin.Phase {
	return []plugin.Phase{plugin.PhasePre, plugin.PhasePost}
}

// Execute pre-debits at ingress (Pre) or settles+records on completion (Post).
func (p *Plugin) Execute(c *plugin.Context, phase plugin.Phase) error {
	if phase == plugin.PhasePost {
		return p.settle(c)
	}
	return p.reserve(c)
}

// reserve pre-debits the completion-only estimate across the caller's scopes,
// all-or-nothing (ADR-0013). Insufficient balance ⇒ reject 402; store
// unreachable ⇒ reject 503 (fail-closed). The reserved amount is stashed on the
// Context for Post to settle.
func (p *Plugin) reserve(c *plugin.Context) error {
	if c.PricingSnapshot == nil {
		c.PricingSnapshot = p.dyn()
	}
	est := p.estimate(c)
	c.Reserved = est
	if p.accounting != nil {
		return p.reserveAccounted(c)
	}

	ok, err := p.quota.TryDebit(c.Ctx, scopesOf(c), est)
	if err != nil {
		// Fail closed: a quota-store outage rejects rather than serves (quota is
		// money; serving on an outage means uncontrolled spend).
		c.Stop = true
		c.BlockedBy = p.Name()
		c.RejectStatus = http.StatusServiceUnavailable
		return nil
	}
	if !ok {
		c.Stop = true
		c.BlockedBy = p.Name()
		c.RejectStatus = http.StatusPaymentRequired
	}
	return nil
}

// settle reconciles the reservation to the actual cost and records usage. It
// ALWAYS settles (delta = reserved − actual): on no usage actual is 0, so the
// full reservation is refunded — never a no-op, which would leak the pre-debit
// (ADR-0013/0016).
//
// B1 fix: the Settle/Record calls run with a context DERIVED FROM but DETACHED
// FROM the cancellation of c.Ctx (the request context). A mid-stream client
// disconnect cancels c.Ctx, but the refund must still land — quota is money.
// We preserve the deadline (if any) as an upper bound but drop cancellation.
func (p *Plugin) settle(c *plugin.Context) error {
	if c.BillingDone {
		return nil
	}
	ctx, cancel := settleContext(c.Ctx)
	defer cancel()
	var actual int64
	usage := usageOf(c)
	pricing, pricingOK := p.pricingFor(c)
	if usage != nil && pricingOK {
		if p.accounting != nil {
			var err error
			actual, err = checkedCost(usage, pricing)
			if err != nil {
				c.BillingUncertain = true
			}
		} else {
			actual = Cost(usage, pricing)
		}
	}

	if p.accounting != nil {
		if c.ReservationID == "" {
			return nil // Pre did not reserve, so there is nothing to refund.
		}
		outcome, reason := "known", ""
		if !c.ChargePossible && usage == nil {
			outcome = "released"
		} else if c.BillingUncertain || usage == nil || !pricingOK || actual < 0 {
			outcome, reason = "unknown", "upstream usage or final charge is not confirmed"
		}
		// In accounting mode the usage row is persisted inside the reservation's
		// settlement transaction (exactly once, via the reservation's durable
		// result and its recovery replay). The fail-open async recorder is
		// bypassed entirely so a crash between settlement and the async flush
		// can never lose the ledger row the E0 attribution views count on.
		var record *UsageRecord
		if outcome == "known" && usage != nil && pricingOK {
			r := p.usageRecord(c, actual, pricing)
			record = &r
		}
		if outcome != "known" {
			actual = 0 // unknown is a state, never a zero-cost accounting fact
		}
		if err := p.accounting.Finish(ctx, Settlement{
			ReservationID: c.ReservationID, Outcome: outcome, Actual: actual, Reason: reason,
			AttemptRisk: outcome == "known" && c.BillingAttemptRisk, Usage: record,
		}); err != nil {
			return err
		}
		c.BillingDone = true
		return nil
	}

	// Legacy path (no accounting coordinator): settle the pre-debit difference
	// and record usage through the fail-open async recorder.
	if delta := c.Reserved - actual; delta != 0 {
		if err := p.quota.Settle(ctx, scopesOf(c), delta); err != nil {
			return err
		}
	}
	if usage == nil {
		return nil // nothing delivered → no usage record
	}
	return p.recorder.Record(ctx, p.usageRecord(c, actual, pricing))
}

func (p *Plugin) usageRecord(c *plugin.Context, actual int64, pricing config.Pricing) UsageRecord {
	usage := usageOf(c)
	// Cache discount reporting: how much the cache multiplier saved vs full price.
	// Zero when no cache hit, no pricing resolved, or multiplier unconfigured
	// (= full price). FullCost - Cost is non-negative whenever pricing resolved.
	discount := int64(0)
	if usage.CachedPromptTokens > 0 {
		discount = FullCost(usage, pricing) - actual
		if discount < 0 {
			discount = 0 // defensive: should not happen
		}
	}
	return UsageRecord{
		ApplicationID:       c.ApplicationID,
		Environment:         c.Environment,
		Currency:            strings.ToLower(pricing.Currency),
		Tenant:              c.Tenant,
		Group:               c.Group,
		APIKeyID:            c.APIKeyID,
		Provider:            c.Provider,
		ProviderEndpoint:    c.ProviderEndpoint,
		Model:               c.Request.Model,
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		Cost:                actual,
		CachedPromptTokens:  usage.CachedPromptTokens,
		CacheDiscountMicros: discount,
		RequestID:           c.RequestID,
		SessionID:           c.SessionID,
		TraceID:             c.TraceID,
	}
}

// settleContext returns a context and cancel func. The context inherits the
// parent's values and deadline (if any) but is NOT cancelled when the parent is
// cancelled. This lets the Post-phase settlement run to completion after a
// client disconnect. The settleTimeout caps the total work so a stuck store
// can't hang forever. The caller MUST defer the returned cancel.
func settleContext(parent context.Context) (ctx context.Context, cancel context.CancelFunc) {
	ctx = context.WithoutCancel(parent)
	// Preserve the parent's deadline (if any) as an upper bound so a slow store
	// still times out; otherwise cap at settleTimeout.
	if dl, ok := parent.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 && remaining < settleTimeout {
			return context.WithTimeout(ctx, remaining)
		}
	}
	return context.WithTimeout(ctx, settleTimeout)
}

// estimate is the completion-only reservation ceiling (ADR-0013): the hit
// provider is unknown before dispatch and there is no prompt tokenizer, so
// est = effectiveMaxTokens × (max completion rate across the alias's
// candidates). effectiveMaxTokens is the request's max_tokens if set, else the
// max DefaultMaxTokens across candidates, else the global maxTokensCeiling — so
// the estimate is never 0 when a rate exists (which would bypass the quota
// pre-debit). Prompt cost is charged exactly at Post.
func (p *Plugin) estimate(c *plugin.Context) int64 {
	maxRate, maxDefault := int64(0), int64(0)
	snapshot := c.PricingSnapshot
	if snapshot == nil {
		snapshot = p.dyn()
	}
	for _, m := range snapshot.Models {
		if m.Alias != c.Request.Model {
			continue
		}
		for _, u := range m.Upstreams {
			maxRate = max(maxRate, u.Pricing.CompletionPer1M)
			maxDefault = max(maxDefault, int64(u.DefaultMaxTokens))
		}
	}
	effTokens := maxDefault
	if effTokens == 0 {
		effTokens = p.maxTokensCeiling // global fallback; closes the est=0 bypass
	}
	if c.Request.MaxTokens != nil {
		effTokens = int64(*c.Request.MaxTokens)
	}
	// Reject an overflowing request rather than wrapping a monetary amount.
	if effTokens < 0 || maxRate < 0 || (maxRate > 0 && effTokens > (math.MaxInt64-500_000)/maxRate) {
		return -1
	}
	return (effTokens*maxRate + 500_000) / 1_000_000
}

// pricingFor resolves the hit provider's pricing for the request's alias.
func (p *Plugin) pricingFor(c *plugin.Context) (config.Pricing, bool) {
	snapshot := c.PricingSnapshot
	if snapshot == nil {
		snapshot = p.dyn()
	}
	mu, ok := snapshot.ResolveModel(c.Request.Model, c.Provider)
	if !ok {
		return config.Pricing{}, false
	}
	return mu.Pricing, true
}

// usageOf returns the response usage, or nil if absent.
func usageOf(c *plugin.Context) *adapter.Usage {
	if c.Response == nil {
		return nil
	}
	return c.Response.Usage
}

// scopesOf returns the quota scopes for the caller's identity (tenant/group/
// key). Empty identity fields are skipped.
func scopesOf(c *plugin.Context) []string {
	var out []string
	if c.Tenant != "" {
		out = append(out, "tenant:"+c.Tenant)
	}
	if c.Group != "" {
		out = append(out, GroupScope(c.Tenant, c.Group))
	}
	if c.APIKeyID != "" {
		out = append(out, "key:"+c.APIKeyID)
	}
	return out
}
