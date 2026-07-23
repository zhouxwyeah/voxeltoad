package proxy

import (
	"fmt"

	"voxeltoad/internal/adapter"
	_ "voxeltoad/internal/adapter/claude" // register claude adapter (adapter.New("claude", ...))
	_ "voxeltoad/internal/adapter/openai" // register openai adapter (adapter.New("openai", ...))
	"voxeltoad/internal/config"
)

// BuildDispatcher assembles a Dispatcher from a dynamic config snapshot: for
// each provider it resolves the shared API-key secret, then for each of the
// provider's endpoints it constructs the endpoint's adapter (by
// ProviderEndpoint.Adapter) and builds a single-endpoint Forwarder with that
// endpoint's timeouts; then it wires the routes and per-candidate model
// preparation (ADR-0002/0009/0011/0049).
//
// One provider yields one Forwarder per endpoint, keyed by EndpointKey — the
// multi-endpoint model (ADR-0049). The shared credential is resolved once per
// provider and reused for every endpoint adapter.
//
// It is pure (config in, Dispatcher out) so the data plane can rebuild and
// atomically swap it on every config-snapshot version change (the architecture's
// hot-reload model; see design/architecture.md).
func BuildDispatcher(dyn *config.Dynamic, cfg DispatcherConfig) (*Dispatcher, error) {
	forwarders := make(map[EndpointKey]*Forwarder)
	for _, p := range dyn.Providers {
		apiKey, err := config.ResolveSecret(p.APIKeyRef)
		if err != nil {
			return nil, fmt.Errorf("proxy: provider %q: %w", p.Name, err)
		}
		for _, ep := range p.Endpoints {
			a, err := adapter.New(ep.Adapter, adapter.Options{BaseURL: ep.BaseURL, APIKey: apiKey})
			if err != nil {
				return nil, fmt.Errorf("proxy: provider %q endpoint %q: %w", p.Name, ep.EndpointID(), err)
			}
			timeouts := p.Timeouts
			if ep.Timeouts != nil {
				timeouts = *ep.Timeouts
			}
			forwarders[EndpointKey{Provider: p.Name, Endpoint: ep.EndpointID()}] = NewForwarder(a, timeouts)
		}
	}

	disp := NewDispatcher(dyn.Routes, forwarders, cfg)
	disp.WithModelPreparation(dyn)
	return disp, nil
}
