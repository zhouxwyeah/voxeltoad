package config

// ResolveModel returns the upstream realization of a model alias on a specific
// provider (ADR-0002): given the client's alias and the provider chosen by
// routing, it yields that provider's native model name and defaults. ok is
// false if the alias is unknown or the provider does not serve it.
func (d *Dynamic) ResolveModel(alias, provider string) (ModelUpstream, bool) {
	for _, m := range d.Models {
		if m.Alias != alias {
			continue
		}
		for _, u := range m.Upstreams {
			if u.Provider == provider {
				return u, true
			}
		}
	}
	return ModelUpstream{}, false
}
