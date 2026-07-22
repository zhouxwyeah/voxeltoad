package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDynamicSchema_JSONRoundTrip guards the admin<->proxy contract: a fully
// populated Dynamic must marshal and unmarshal back to an equal value, so both
// planes agree on the snapshot wire format.
func TestDynamicSchema_JSONRoundTrip(t *testing.T) {
	orig := Dynamic{
		Version: "v42",
		Providers: []Provider{{
			Name: "openai-prod",
			Type: "openai",
			Endpoints: []ProviderEndpoint{{
				ID: "openai", Adapter: "openai", BaseURL: "https://api.openai.com/v1",
			}},
			APIKeyRef: "env://OPENAI_KEY",
			Timeouts:  ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 10 * time.Second, Overall: 5 * time.Minute},
			Weight:    10,
		}},
		Models: []Model{{
			Alias: "default-chat",
			Upstreams: []ModelUpstream{{
				Provider:         "openai-prod",
				UpstreamModel:    "gpt-4o",
				DefaultMaxTokens: 4096,
				Pricing:          Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000, Currency: "USD", CacheHitMultiplier: 500_000},
			}},
		}},
		Routes: []Route{{
			ModelAlias: "default-chat",
			Providers:  []RouteProvider{{Name: "openai-prod", Weight: 3}, {Name: "openai-backup", Weight: 1}},
			Strategy:   "priority",
		}},
		Plugins: []PluginConfig{{
			Name:    "ratelimit",
			Phase:   "pre",
			Params:  map[string]any{"rps": float64(100)},
			Enabled: true,
			Scope:   "",
		}},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Dynamic
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Errorf("round-trip mismatch:\n orig = %+v\n got  = %+v", orig, got)
	}
}

// TestDynamicSchema_EmptyOmitsSections ensures an empty Dynamic stays compact on
// the wire (omitempty), so a "no config yet" snapshot is just its version.
func TestDynamicSchema_EmptyOmitsSections(t *testing.T) {
	data, err := json.Marshal(Dynamic{Version: "v0"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); got != `{"version":"v0"}` {
		t.Errorf("empty Dynamic = %s, want {\"version\":\"v0\"}", got)
	}
}

// TestModel_MetadataRoundTrip verifies the catalog metadata fields
// (description, context_length, capabilities, tags) survive a JSON round-trip,
// and that a model with zero metadata omits them (omitempty) so old snapshots
// without these fields still parse cleanly.
func TestModel_MetadataRoundTrip(t *testing.T) {
	orig := Model{
		Alias:         "gpt-4o",
		Description:   "Multimodal flagship model",
		ContextLength: 128000,
		Capabilities:  []string{"vision", "function_calling", "streaming"},
		Tags:          []string{"chat", "reasoning"},
		Upstreams: []ModelUpstream{{
			Provider:      "openai-prod",
			UpstreamModel: "gpt-4o",
			Pricing:       Pricing{PromptPer1M: 5_000_000, CompletionPer1M: 15_000_000, Currency: "USD", CacheHitMultiplier: 500_000},
		}},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Model
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Errorf("round-trip mismatch:\n orig = %+v\n got  = %+v", orig, got)
	}

	// A bare alias-only model (the pre-metadata shape) must still parse and
	// have empty metadata — backward compatibility for old spec JSONB.
	var legacy Model
	if err := json.Unmarshal([]byte(`{"alias":"old","upstreams":[]}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.Description != "" || legacy.ContextLength != 0 || len(legacy.Capabilities) != 0 || len(legacy.Tags) != 0 {
		t.Errorf("legacy model should have empty metadata, got %+v", legacy)
	}
}

// TestPricing_CacheHitMultiplier_OmitEmpty verifies that a zero
// CacheHitMultiplier is omitted from the JSON wire form, so legacy snapshots
// (and snapshots from older admin planes that don't know the field) round-trip
// without spurious diff. A populated value must round-trip exactly.
func TestPricing_CacheHitMultiplier_OmitEmpty(t *testing.T) {
	// Zero value → omitted.
	zero := Pricing{PromptPer1M: 1, CompletionPer1M: 2, Currency: "USD"}
	data, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "cache_hit_multiplier") {
		t.Errorf("zero CacheHitMultiplier should be omitted, got %s", data)
	}

	// Non-zero → round-trips.
	withCache := Pricing{PromptPer1M: 1, CompletionPer1M: 2, Currency: "USD", CacheHitMultiplier: 100_000}
	data, err = json.Marshal(withCache)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Pricing
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CacheHitMultiplier != 100_000 {
		t.Errorf("CacheHitMultiplier round-trip = %d, want 100000", got.CacheHitMultiplier)
	}

	// Legacy JSON without the field parses with zero (backward compatible).
	var legacy Pricing
	if err := json.Unmarshal([]byte(`{"prompt_per_1m":1,"completion_per_1m":2,"currency":"USD"}`), &legacy); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if legacy.CacheHitMultiplier != 0 {
		t.Errorf("legacy CacheHitMultiplier = %d, want 0 (treated as full price)", legacy.CacheHitMultiplier)
	}
}
