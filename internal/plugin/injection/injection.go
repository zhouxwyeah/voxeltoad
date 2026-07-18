// Package injection provides a Pre-phase governance plugin that detects
// common prompt-injection and jailbreak patterns (e.g. "ignore previous
// instructions", role override, DAN variants). On detection it short-circuits
// the request with Stop=true, BlockedBy="injection".
//
// Detection is heuristic and regex-based — false positives are possible on
// benign text that coincidentally contains injection keywords. Operators may
// configure the pattern whitelist per deployment.
//
// Privacy: only BlockedBy="injection" is recorded. Message content is never
// logged (ADR-0021).
package injection

import (
	"fmt"
	"regexp"
	"strings"

	"voxeltoad/internal/plugin"
)

const name = "injection"

// Config describes the configuration for the prompt injection plugin.
type Config struct {
	Enabled  bool     `json:"enabled"`
	Patterns []string `json:"patterns,omitempty"` // whitelist; empty = all
}

// Plugin implements plugin.Plugin for prompt injection detection.
type Plugin struct {
	patterns []string // enabled pattern names
	enabled  bool
}

// NewPlugin builds an injection Plugin from a Config.
func NewPlugin(cfg Config) *Plugin {
	patterns := cfg.Patterns
	if len(patterns) == 0 {
		patterns = []string{"ignore_instructions", "role_override", "dan_jailbreak", "system_override"}
	}
	return &Plugin{patterns: patterns, enabled: cfg.Enabled}
}

func (p *Plugin) Name() string           { return name }
func (p *Plugin) Phases() []plugin.Phase { return []plugin.Phase{plugin.PhasePre} }

func (p *Plugin) Execute(c *plugin.Context, phase plugin.Phase) error {
	if !p.enabled || phase != plugin.PhasePre || c.Request == nil {
		return nil
	}
	for _, msg := range c.Request.Messages {
		if msg.Content.Text() == "" {
			continue
		}
		if p.detect(msg.Content.Text()) {
			c.Stop = true
			c.BlockedBy = name
			return nil
		}
	}
	return nil
}

// --- Detection patterns ---

type injectionPattern struct {
	name string
	re   *regexp.Regexp
}

var patterns = map[string]injectionPattern{
	"ignore_instructions": {
		name: "ignore_instructions",
		re:   regexp.MustCompile(`(?i)(ignore|forget|disregard)\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|rules?|commands?|context)`),
	},
	"role_override": {
		name: "role_override",
		re:   regexp.MustCompile(`(?i)(you\s+are\s+now\s+(a\s+)?|you\s+will\s+now\s+act\s+as\s+|from\s+now\s+on\s+you\s+are\s+)(DAN|developer|unrestricted|unfiltered|evil|malicious)`),
	},
	"dan_jailbreak": {
		name: "dan_jailbreak",
		re:   regexp.MustCompile(`(?i)\b(DAN|Do\s+Anything\s+Now|jailbreak|jail\s*break)\b`),
	},
	"system_override": {
		name: "system_override",
		re:   regexp.MustCompile(`(?i)(your\s+system\s+prompt\s+(is|was|has\s+been)|overriding?\s+(the\s+)?system\s+prompt|new\s+system\s+prompt\s*[:=])`),
	},
}

func (p *Plugin) detect(text string) bool {
	lower := strings.ToLower(text)
	for _, name := range p.patterns {
		pat, ok := patterns[name]
		if !ok {
			continue
		}
		if pat.re.MatchString(lower) {
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
