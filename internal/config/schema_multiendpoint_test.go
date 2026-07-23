package config

import (
	"encoding/json"
	"testing"
	"time"
)

// TestProvider_MultiEndpoint_JSONRoundTrip verifies the multi-endpoint Provider
// shape (ADR-0049): Endpoints carry the (adapter, base_url) pairs; the provider
// carries shared credential + defaults; JSON round-trips losslessly.
func TestProvider_MultiEndpoint_JSONRoundTrip(t *testing.T) {
	orig := Provider{
		Name: "deepseek-prod",
		Type: "deepseek",
		Endpoints: []ProviderEndpoint{
			{ID: "openai", Adapter: "openai", BaseURL: "https://api.deepseek.com/v1"},
			{ID: "anthropic", Adapter: "claude", BaseURL: "https://api.deepseek.com/anthropic",
				Timeouts: &ProviderTimeouts{Connect: 5 * time.Second, FirstByte: 60 * time.Second, Overall: 10 * time.Minute}},
		},
		APIKeyRef: "db://provider/deepseek-prod",
		Timeouts:  ProviderTimeouts{Connect: 3 * time.Second, FirstByte: 30 * time.Second, Overall: 5 * time.Minute},
		Weight:    100,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Provider
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(got.Endpoints))
	}
	if got.Endpoints[0].Adapter != "openai" || got.Endpoints[0].BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("endpoint[0] = %+v", got.Endpoints[0])
	}
	if got.Endpoints[1].Adapter != "claude" {
		t.Errorf("endpoint[1].adapter = %q, want claude", got.Endpoints[1].Adapter)
	}
	if got.Endpoints[1].Timeouts == nil || got.Endpoints[1].Timeouts.FirstByte != 60*time.Second {
		t.Errorf("endpoint[1].timeouts = %+v, want override FirstByte=60s", got.Endpoints[1].Timeouts)
	}
	if got.Endpoints[0].Timeouts != nil {
		t.Errorf("endpoint[0].timeouts = %+v, want nil (inherit provider default)", got.Endpoints[0].Timeouts)
	}
	if got.APIKeyRef != "db://provider/deepseek-prod" {
		t.Errorf("apiKeyRef = %q", got.APIKeyRef)
	}
}

// TestProviderEndpoint_PrimaryAdapter verifies PrimaryAdapter returns the first
// endpoint's adapter (used for the providers.adapter promoted column).
func TestProviderEndpoint_PrimaryAdapter(t *testing.T) {
	p := Provider{Endpoints: []ProviderEndpoint{
		{ID: "openai", Adapter: "openai", BaseURL: "https://a"},
		{ID: "anthropic", Adapter: "claude", BaseURL: "https://b"},
	}}
	if got := p.PrimaryAdapter(); got != "openai" {
		t.Errorf("PrimaryAdapter = %q, want openai", got)
	}
	empty := Provider{}
	if got := empty.PrimaryAdapter(); got != "" {
		t.Errorf("empty PrimaryAdapter = %q, want \"\"", got)
	}
}

// TestValidateProvider_Endpoints verifies provider validation: at least one
// endpoint, unique endpoint ids, known adapter, non-empty base_url.
func TestValidateProvider_Endpoints(t *testing.T) {
	cases := []struct {
		name    string
		p       Provider
		wantErr bool
	}{
		{"empty endpoints", Provider{Name: "x", Type: "openai"}, true},
		{"valid single", Provider{Name: "x", Type: "openai", Endpoints: []ProviderEndpoint{{ID: "openai", Adapter: "openai", BaseURL: "https://a"}}}, false},
		{"valid multi", Provider{Name: "x", Type: "openai", Endpoints: []ProviderEndpoint{
			{ID: "openai", Adapter: "openai", BaseURL: "https://a"},
			{ID: "anthropic", Adapter: "claude", BaseURL: "https://b"},
		}}, false},
		{"dup endpoint id", Provider{Name: "x", Type: "openai", Endpoints: []ProviderEndpoint{
			{ID: "openai", Adapter: "openai", BaseURL: "https://a"},
			{ID: "openai", Adapter: "openai", BaseURL: "https://b"},
		}}, true},
		{"unknown adapter", Provider{Name: "x", Type: "openai", Endpoints: []ProviderEndpoint{{ID: "e", Adapter: "gemini", BaseURL: "https://a"}}}, true},
		{"empty base_url", Provider{Name: "x", Type: "openai", Endpoints: []ProviderEndpoint{{ID: "e", Adapter: "openai", BaseURL: ""}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateProvider(&c.p)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateProvider err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}
