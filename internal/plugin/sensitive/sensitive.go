// Package sensitive provides a Pre-phase governance plugin that checks LLM
// request messages against a configurable word list. On a match the plugin
// short-circuits the request (c.Stop=true, c.BlockedBy="sensitive").
//
// The plugin is registered via init() and follows the plugin.Factory pattern
// (design/architecture.md "新增一个治理插件").
//
// Key constraints (design/plan §5.4): The plugin records only semantic fields
// (BlockedBy="sensitive" + match count via llm.plugin.blocked_by). It NEVER
// logs request content — privacy and volume constraints apply (ADR-0021 §2).
package sensitive

import (
	"fmt"
	"strings"

	"voxeltoad/internal/plugin"
)

// name is the plugin identifier used in configuration and registration.
const name = "sensitive"

// Config describes the configuration for the sensitive word plugin.
type Config struct {
	// Enabled controls whether matching is active. When false, Execute is a no-op.
	Enabled bool `json:"enabled,omitempty"`
	// Words is a list of case-insensitive keywords to match against message content.
	Words []string `json:"words,omitempty"`
}

// Plugin implements the plugin.Plugin interface for sensitive word matching.
type Plugin struct {
	words  []string
	active bool
}

// NewPlugin builds a sensitive Plugin from a Config. It normalizes all words to
// lowercase for case-insensitive matching.
func NewPlugin(cfg Config) *Plugin {
	if len(cfg.Words) == 0 {
		return &Plugin{active: false}
	}
	lower := make([]string, len(cfg.Words))
	for i, w := range cfg.Words {
		lower[i] = strings.ToLower(w)
	}
	return &Plugin{words: lower, active: cfg.Enabled}
}

func (p *Plugin) Name() string           { return name }
func (p *Plugin) Phases() []plugin.Phase { return []plugin.Phase{plugin.PhasePre} }

func (p *Plugin) Execute(c *plugin.Context, phase plugin.Phase) error {
	if !p.active || len(p.words) == 0 {
		return nil
	}
	if phase != plugin.PhasePre {
		return nil
	}
	if c.Request == nil {
		return nil
	}
	for _, msg := range c.Request.Messages {
		if p.matchText(msg.Content.Text()) {
			c.Stop = true
			c.BlockedBy = name
			return nil
		}
	}
	return nil
}

func (p *Plugin) matchText(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, w := range p.words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func init() {
	plugin.Register(name, func(cfg any) (plugin.Plugin, error) {
		c, ok := cfg.(Config)
		if !ok {
			return nil, fmt.Errorf("plugin %s: expected Config, got %T", name, cfg)
		}
		return NewPlugin(c), nil
	})
}
