// Package plugin defines the governance plugin framework. Plugins run in an
// ordered chain around the upstream call: Pre-phase plugins execute before the
// request is forwarded (rate limiting, quota, sensitive-word checks, cache
// lookup) and may short-circuit the request; Post-phase plugins execute after
// the response (audit, billing, response rewriting).
//
// See design/architecture.md ("新增一个治理插件") for the steps to add a plugin.
package plugin

import (
	"context"
	"fmt"
	"sync"

	"voxeltoad/internal/adapter"
)

// Phase identifies when a plugin runs in the request lifecycle.
type Phase int

const (
	// PhasePre runs before the upstream call.
	PhasePre Phase = iota
	// PhasePost runs after the upstream call.
	PhasePost
)

// Context carries per-request state through the plugin chain. Plugins read and
// mutate it to implement their behavior. Setting Stop halts further chain
// execution (e.g. a cache hit or a sensitive-word block).
type Context struct {
	Ctx context.Context

	// Tenant, Group, and APIKeyID identify the caller (populated by auth).
	// The three-level tenancy is Tenant → Group → APIKey (see ADR-0005).
	Tenant   string
	Group    string
	APIKeyID string

	// Request is the unified request; Pre plugins may rewrite it.
	Request *adapter.UnifiedRequest

	// Response is the unified response; populated before Post plugins run.
	Response *adapter.UnifiedResponse

	// Provider is the upstream actually selected (after routing/fallback).
	Provider string

	// RequestID is the gateway-assigned per-request correlation id, populated
	// by the router from trace headers (or generated when absent). Format is
	// chi's request-id (host/random-N), not a ULID. Carried so Post plugins
	// (e.g. billing) can stamp it onto usage_records for session tracing.
	RequestID string
	// SessionID is the client-provided session key (X-Voxeltoad-Session header),
	// populated by the router. Used for session-scoped cost attribution.
	SessionID string
	// SessionSource records which mechanism supplied SessionID (header-config,
	// header-generic, body-session, body-metadata, body-user, prefix, or "").
	// Purely observability — recorded on request_logs, never affects routing.
	SessionSource string
	// TraceID is the W3C trace id parsed from the incoming traceparent header,
	// populated by the router. Carried so Post plugins (e.g. billing) can stamp
	// it onto usage_records, joining gateway costs to upstream distributed traces.
	TraceID string

	// Stop, when set true by a plugin, halts the chain. BlockedBy records
	// which plugin stopped it (empty if not blocked). These map to the
	// llm.plugin.blocked_by observability field (see design/observability.md).
	Stop      bool
	BlockedBy string

	// RejectStatus optionally carries the HTTP status a Pre rejection should
	// produce (e.g. 402 quota exhausted, 503 quota store unreachable). 0 leaves
	// the router's default (429). Only meaningful when Stop is set (ADR-0013).
	RejectStatus int

	// Reserved carries a plugin's pre-reservation amount from Pre to Post (e.g.
	// the billing quota estimate), so Post can reconcile it. Plugin-private by
	// convention; the billing plugin owns it.
	Reserved int64
}

// Plugin is a single governance unit in the request chain. A plugin may act in
// more than one phase (e.g. a billing plugin checks quota in Pre and debits in
// Post); Execute is called once per matching phase, with the active phase
// passed in so the plugin can branch.
type Plugin interface {
	// Name returns the plugin identifier (e.g. "ratelimit", "audit").
	Name() string

	// Phases reports the phases in which the plugin runs.
	Phases() []Phase

	// Execute runs the plugin for the given phase. Returning an error aborts the
	// request; setting c.Stop short-circuits the remaining chain without an
	// error (e.g. cache hit). Only meaningful in Pre.
	Execute(c *Context, phase Phase) error
}

// Factory builds a Plugin from its configuration blob.
type Factory func(cfg any) (Plugin, error)

var (
	mu        sync.RWMutex
	factories = make(map[string]Factory)
)

// Register makes a plugin factory available under name, intended to be called
// from plugin packages' init functions. Registering the same name twice panics.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[name]; dup {
		panic(fmt.Sprintf("plugin: Register called twice for %q", name))
	}
	factories[name] = f
}

// New constructs a registered plugin by name with the given config.
func New(name string, cfg any) (Plugin, error) {
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin: unknown plugin %q", name)
	}
	return f(cfg)
}

// Chain executes plugins of a given phase in order. It stops early (without
// error) when a plugin sets Context.Stop.
type Chain struct {
	plugins []Plugin
}

// NewChain builds a chain from the given plugins. Callers are responsible for
// passing them in the desired execution order.
func NewChain(plugins ...Plugin) *Chain {
	return &Chain{plugins: plugins}
}

// Run executes every plugin that acts in phase, in order, until one sets Stop
// or an error occurs.
func (ch *Chain) Run(c *Context, phase Phase) error {
	for _, p := range ch.plugins {
		if !actsIn(p, phase) {
			continue
		}
		if err := p.Execute(c, phase); err != nil {
			return err
		}
		if c.Stop {
			return nil
		}
	}
	return nil
}

func actsIn(p Plugin, phase Phase) bool {
	for _, ph := range p.Phases() {
		if ph == phase {
			return true
		}
	}
	return false
}
