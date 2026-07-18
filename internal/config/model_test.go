package config

import "testing"

func TestResolveModel(t *testing.T) {
	d := &Dynamic{
		Models: []Model{
			{
				Alias: "default-chat",
				Upstreams: []ModelUpstream{
					{Provider: "openai-prod", UpstreamModel: "gpt-4o", DefaultMaxTokens: 4096},
					{Provider: "anthropic", UpstreamModel: "claude-3-5-sonnet", DefaultMaxTokens: 1024},
				},
			},
		},
	}

	t.Run("resolves per-provider upstream model", func(t *testing.T) {
		mu, ok := d.ResolveModel("default-chat", "openai-prod")
		if !ok {
			t.Fatal("expected resolution")
		}
		if mu.UpstreamModel != "gpt-4o" || mu.DefaultMaxTokens != 4096 {
			t.Errorf("got %+v, want gpt-4o/4096", mu)
		}
	})

	t.Run("same alias, different provider, different upstream", func(t *testing.T) {
		mu, ok := d.ResolveModel("default-chat", "anthropic")
		if !ok {
			t.Fatal("expected resolution")
		}
		if mu.UpstreamModel != "claude-3-5-sonnet" || mu.DefaultMaxTokens != 1024 {
			t.Errorf("got %+v, want claude-3-5-sonnet/1024", mu)
		}
	})

	t.Run("unknown alias", func(t *testing.T) {
		if _, ok := d.ResolveModel("nope", "openai-prod"); ok {
			t.Error("unknown alias should not resolve")
		}
	})

	t.Run("alias exists but provider not mapped", func(t *testing.T) {
		if _, ok := d.ResolveModel("default-chat", "zhipu"); ok {
			t.Error("unmapped provider should not resolve")
		}
	})
}
