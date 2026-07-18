package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/plugin/ratelimit"
)

func ctxWith(tenant, group, key string, usage *adapter.Usage) *plugin.Context {
	c := &plugin.Context{
		Ctx:      context.Background(),
		Tenant:   tenant,
		Group:    group,
		APIKeyID: key,
		Request:  &adapter.UnifiedRequest{Model: "gpt-4o"},
	}
	if usage != nil {
		c.Response = &adapter.UnifiedResponse{Usage: usage}
	}
	return c
}

func newPlugin(limits ratelimit.Limits) (*ratelimit.Plugin, ratelimit.Limiter) {
	lim := ratelimit.NewMemoryLimiter()
	return ratelimit.NewPlugin(lim, limits), lim
}

func TestPlugin_PreMetadata(t *testing.T) {
	p, _ := newPlugin(ratelimit.Limits{TenantRPM: 10, Window: time.Minute})
	if p.Name() != "ratelimit" {
		t.Errorf("Name = %q, want ratelimit", p.Name())
	}
	if got := p.Phases(); len(got) != 2 || got[0] != plugin.PhasePre || got[1] != plugin.PhasePost {
		t.Errorf("Phases = %v, want [Pre Post]", got)
	}
}

func TestPlugin_RPMAllowsThenBlocks(t *testing.T) {
	p, _ := newPlugin(ratelimit.Limits{KeyRPM: 2, Window: time.Minute})

	for i := 0; i < 2; i++ {
		c := ctxWith("acme", "team", "key1", nil)
		if err := p.Execute(c, plugin.PhasePre); err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if c.Stop {
			t.Fatalf("request %d should pass", i)
		}
	}
	// 3rd exceeds key RPM=2.
	c := ctxWith("acme", "team", "key1", nil)
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !c.Stop || c.BlockedBy != "ratelimit" {
		t.Errorf("3rd should be blocked by ratelimit; Stop=%v BlockedBy=%q", c.Stop, c.BlockedBy)
	}
}

// TestPlugin_DimensionsPerIdentity: different keys have independent RPM budgets.
func TestPlugin_DimensionsPerIdentity(t *testing.T) {
	p, _ := newPlugin(ratelimit.Limits{KeyRPM: 1, Window: time.Minute})

	c1 := ctxWith("acme", "team", "key1", nil)
	_ = p.Execute(c1, plugin.PhasePre)
	if c1.Stop {
		t.Fatal("key1 first request should pass")
	}
	// key2 has its own budget.
	c2 := ctxWith("acme", "team", "key2", nil)
	_ = p.Execute(c2, plugin.PhasePre)
	if c2.Stop {
		t.Error("key2 should have an independent budget")
	}
}

// TestPlugin_TenantSharedAcrossKeys: a tenant-level limit is shared by all keys.
func TestPlugin_TenantSharedAcrossKeys(t *testing.T) {
	p, _ := newPlugin(ratelimit.Limits{TenantRPM: 1, Window: time.Minute})

	c1 := ctxWith("acme", "team", "key1", nil)
	_ = p.Execute(c1, plugin.PhasePre)
	if c1.Stop {
		t.Fatal("first request should pass")
	}
	// Different key, same tenant — tenant budget exhausted.
	c2 := ctxWith("acme", "team", "key2", nil)
	_ = p.Execute(c2, plugin.PhasePre)
	if !c2.Stop {
		t.Error("tenant RPM=1 should block a second request even from another key")
	}
}

// TestPlugin_TPMIngressAllowThenDebit: ingress passes when under TPM; the debit
// (post phase) records real usage so a later ingress is rejected once over.
func TestPlugin_TPMIngressAllowThenDebit(t *testing.T) {
	p, _ := newPlugin(ratelimit.Limits{TenantTPM: 100, Window: time.Minute})

	// Ingress 1: window empty → pass.
	c1 := ctxWith("acme", "team", "key1", nil)
	if err := p.Execute(c1, plugin.PhasePre); err != nil || c1.Stop {
		t.Fatalf("ingress1 should pass: err=%v stop=%v", err, c1.Stop)
	}
	// Post: response consumed 120 tokens → debit.
	c1.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{TotalTokens: 120}}
	if err := p.Execute(c1, plugin.PhasePost); err != nil {
		t.Fatalf("debit: %v", err)
	}
	// Ingress 2: window now 120 > 100 → blocked.
	c2 := ctxWith("acme", "team", "key1", nil)
	_ = p.Execute(c2, plugin.PhasePre)
	if !c2.Stop {
		t.Error("ingress2 should be blocked: TPM window already over limit")
	}
}

func TestPlugin_NoLimitsConfigured_AlwaysPasses(t *testing.T) {
	p, _ := newPlugin(ratelimit.Limits{}) // all zero = unlimited
	for i := 0; i < 5; i++ {
		c := ctxWith("acme", "team", "key1", nil)
		_ = p.Execute(c, plugin.PhasePre)
		if c.Stop {
			t.Fatalf("request %d should pass when no limits set", i)
		}
	}
}
