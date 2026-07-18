package ratelimit

import (
	"time"

	"voxeltoad/internal/plugin"
)

// Limits describes the per-identity rate limits for the plugin, sourced from
// config. A zero value for any field means "no limit" on that dimension.
// Limits apply at the tenant, group, and key levels (ADR-0005).
type Limits struct {
	TenantRPM int
	TenantTPM int
	GroupRPM  int
	GroupTPM  int
	KeyRPM    int
	KeyTPM    int
	// Window is the sliding window for all dimensions (default 1m if zero).
	Window time.Duration
}

// Plugin enforces rate limits in the Pre phase using a Limiter. RPM dimensions
// are charged at ingress (n=1); TPM dimensions use allow-then-debit — ingress
// only checks "already over?" (n=0) and Debit records the real usage in the
// Post phase (ADR-0008).
type Plugin struct {
	lim    Limiter
	limits Limits
	window time.Duration
}

// NewPlugin wraps a Limiter as a governance plugin with the given limits.
func NewPlugin(lim Limiter, limits Limits) *Plugin {
	w := limits.Window
	if w <= 0 {
		w = time.Minute
	}
	return &Plugin{lim: lim, limits: limits, window: w}
}

// Name identifies the plugin.
func (p *Plugin) Name() string { return "ratelimit" }

// Phases: rate limiting checks at ingress (Pre) and debits actual TPM usage on
// completion (Post) — the allow-then-debit closure (ADR-0008).
func (p *Plugin) Phases() []plugin.Phase { return []plugin.Phase{plugin.PhasePre, plugin.PhasePost} }

// Execute checks dimensions at ingress (Pre) or debits real usage (Post).
func (p *Plugin) Execute(c *plugin.Context, phase plugin.Phase) error {
	if phase == plugin.PhasePost {
		return p.debit(c)
	}
	return p.check(c)
}

// check enforces all configured dimensions at ingress. RPM dims are charged
// (n=1); TPM dims are only checked (n=0, allow-then-debit). On rejection it
// stops the chain.
func (p *Plugin) check(c *plugin.Context) error {
	rpmDims := p.dimensions(c, RPM)
	if len(rpmDims) > 0 {
		dec, err := p.lim.Allow(c.Ctx, rpmDims, 1)
		if err != nil {
			return err
		}
		if !dec.OK {
			c.Stop = true
			c.BlockedBy = p.Name()
			return nil
		}
	}

	tpmDims := p.dimensions(c, TPM)
	if len(tpmDims) > 0 {
		dec, err := p.lim.Allow(c.Ctx, tpmDims, 0) // check only
		if err != nil {
			return err
		}
		if !dec.OK {
			c.Stop = true
			c.BlockedBy = p.Name()
			return nil
		}
	}
	return nil
}

// debit records actual token usage against the TPM dimensions after the
// response (allow-then-debit reconciliation).
func (p *Plugin) debit(c *plugin.Context) error {
	if c.Response == nil || c.Response.Usage == nil {
		return nil
	}
	tpmDims := p.dimensions(c, TPM)
	if len(tpmDims) == 0 {
		return nil
	}
	return p.lim.Debit(c.Ctx, tpmDims, c.Response.Usage.TotalTokens)
}

// dimensions builds the limiter dimensions for the given metric from the
// caller's identity and the configured limits (zero limit = dimension omitted).
func (p *Plugin) dimensions(c *plugin.Context, m Metric) []Dimension {
	var dims []Dimension
	add := func(scopePrefix, scopeVal string, limit int) {
		if limit > 0 && scopeVal != "" {
			dims = append(dims, Dimension{
				Scope:  scopePrefix + ":" + scopeVal,
				Metric: m,
				Limit:  limit,
				Window: p.window,
			})
		}
	}
	switch m {
	case RPM:
		add("tenant", c.Tenant, p.limits.TenantRPM)
		add("group", c.Group, p.limits.GroupRPM)
		add("key", c.APIKeyID, p.limits.KeyRPM)
	case TPM:
		add("tenant", c.Tenant, p.limits.TenantTPM)
		add("group", c.Group, p.limits.GroupTPM)
		add("key", c.APIKeyID, p.limits.KeyTPM)
	}
	return dims
}
