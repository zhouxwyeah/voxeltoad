package proxy

import (
	"fmt"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

// BuildDispatcher assembles a Dispatcher from a dynamic config snapshot: for
// each provider it resolves the API-key secret, constructs the adapter from the
// registry (by Provider.Adapter), and builds a single-provider Forwarder with
// that provider's layered timeouts; then it wires the routes and per-candidate
// model preparation (ADR-0002/0009/0011).
//
// It is pure (config in, Dispatcher out) so the data plane can rebuild and
// atomically swap it on every config-snapshot version change (the architecture's
// hot-reload model; see design/architecture.md).
func BuildDispatcher(dyn *config.Dynamic, cfg DispatcherConfig) (*Dispatcher, error) {
	forwarders := make(map[string]*Forwarder, len(dyn.Providers))
	for _, p := range dyn.Providers {
		apiKey, err := config.ResolveSecret(p.APIKeyRef)
		if err != nil {
			return nil, fmt.Errorf("proxy: provider %q: %w", p.Name, err)
		}
		a, err := adapter.New(p.Adapter, adapter.Options{BaseURL: p.BaseURL, APIKey: apiKey})
		if err != nil {
			return nil, fmt.Errorf("proxy: provider %q: %w", p.Name, err)
		}
		forwarders[p.Name] = NewForwarder(a, p.Timeouts)
	}

	disp := NewDispatcher(dyn.Routes, forwarders, cfg)
	disp.WithModelPreparation(dyn)
	return disp, nil
}
