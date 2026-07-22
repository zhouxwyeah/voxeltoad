package proxy

import (
	"fmt"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/normalize"
)

// modelPreparer turns a client request (carrying a model alias) into a
// provider-endpoint-specific upstream request: it resolves the alias to the
// endpoint's native upstream model (ADR-0002) and normalizes the request for
// that endpoint's adapter protocol (ADR-0009). Run per failover candidate so
// a cross-protocol failover (e.g. OpenAI → Claude) normalizes correctly for
// each. In the multi-endpoint model (ADR-0049) the candidate is a
// (provider, endpoint) pair; the endpoint's adapter drives normalization.
type modelPreparer struct {
	dyn          *config.Dynamic
	endpointsByPvd map[string][]config.ProviderEndpoint // provider name → its endpoints (ordered)
}

func newModelPreparer(dyn *config.Dynamic) *modelPreparer {
	byPvd := make(map[string][]config.ProviderEndpoint, len(dyn.Providers))
	for _, p := range dyn.Providers {
		byPvd[p.Name] = p.Endpoints
	}
	return &modelPreparer{dyn: dyn, endpointsByPvd: byPvd}
}

// pickEndpoint selects the endpoint to use for a request to the given provider
// under the given ingress protocol (ADR-0049): the endpoint whose adapter
// matches the ingress protocol's adapter family, else the provider's first
// (primary) endpoint. Returns ok=false when the provider has no endpoints.
func (p *modelPreparer) pickEndpoint(provider, ingressProtocol string) (config.ProviderEndpoint, bool) {
	eps, ok := p.endpointsByPvd[provider]
	if !ok || len(eps) == 0 {
		return config.ProviderEndpoint{}, false
	}
	if ingressProtocol != "" {
		want := ingressAdapterName(ingressProtocol)
		for _, ep := range eps {
			if ep.Adapter == want {
				return ep, true
			}
		}
	}
	return eps[0], true
}

// adapterFor returns the adapter name of the given provider endpoint, "" when
// unknown. Used by the dispatcher's protocol-aware endpoint selection and the
// router's health filter.
func (p *modelPreparer) adapterFor(provider, endpointID string) string {
	eps, ok := p.endpointsByPvd[provider]
	if !ok {
		return ""
	}
	for _, ep := range eps {
		if ep.EndpointID() == endpointID {
			return ep.Adapter
		}
	}
	return ""
}

// ingressAdapterName maps an ingress protocol name to the adapter family that
// speaks it ("anthropic" → "claude", "openai" → "openai"). Kept local to avoid
// an import cycle with internal/ingress (proxy → ingress for Codec, but
// preparer must not depend on the codec registry for a static mapping).
func ingressAdapterName(ingressProtocol string) string {
	if ingressProtocol == "anthropic" {
		return "claude"
	}
	return ingressProtocol
}

// Prepare returns a request ready for the given provider endpoint: alias
// resolved to the upstream model name and normalized for that endpoint's
// adapter. It does not mutate the input.
func (p *modelPreparer) Prepare(req *adapter.UnifiedRequest, provider, endpointID string) (*adapter.UnifiedRequest, error) {
	mu, ok := p.dyn.ResolveModel(req.Model, provider)
	if !ok {
		return nil, fmt.Errorf("proxy: provider %q does not serve model %q", provider, req.Model)
	}

	target := normalize.Target{DefaultMaxTokens: mu.DefaultMaxTokens}
	if p.adapterFor(provider, endpointID) == "claude" {
		target.CollapseSystem = true
		target.RequireAlternation = true
	}

	out := normalize.Apply(req, target) // returns a copy; input untouched
	out.Model = mu.UpstreamModel        // alias → provider-native upstream name
	return out, nil
}
