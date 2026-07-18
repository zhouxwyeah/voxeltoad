package proxy

import (
	"fmt"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/normalize"
)

// modelPreparer turns a client request (carrying a model alias) into a
// provider-specific upstream request: it resolves the alias to the provider's
// native upstream model (ADR-0002) and normalizes the request for that
// provider's adapter protocol (ADR-0009). Run per failover candidate so a
// cross-protocol failover (e.g. OpenAI → Claude) normalizes correctly for each.
type modelPreparer struct {
	dyn          *config.Dynamic
	adapterByPvd map[string]string // provider name → adapter type
}

func newModelPreparer(dyn *config.Dynamic) *modelPreparer {
	byPvd := make(map[string]string, len(dyn.Providers))
	for _, p := range dyn.Providers {
		byPvd[p.Name] = p.Adapter
	}
	return &modelPreparer{dyn: dyn, adapterByPvd: byPvd}
}

// Prepare returns a request ready for the given provider: alias resolved to the
// upstream model name and normalized for that provider's adapter. It does not
// mutate the input.
func (p *modelPreparer) Prepare(req *adapter.UnifiedRequest, provider string) (*adapter.UnifiedRequest, error) {
	mu, ok := p.dyn.ResolveModel(req.Model, provider)
	if !ok {
		return nil, fmt.Errorf("proxy: provider %q does not serve model %q", provider, req.Model)
	}

	target := normalize.Target{DefaultMaxTokens: mu.DefaultMaxTokens}
	if p.adapterByPvd[provider] == "claude" {
		target.CollapseSystem = true
		target.RequireAlternation = true
	}

	out := normalize.Apply(req, target) // returns a copy; input untouched
	out.Model = mu.UpstreamModel        // alias → provider-native upstream name
	return out, nil
}
