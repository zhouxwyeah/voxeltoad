// Package pii provides a Pre-phase governance plugin that detects
// personally identifiable information (PII) in LLM request messages and
// optionally redacts or blocks them.
//
// Supported patterns: email, phone (CN/partial), ID card, credit card.
//
// The plugin is registered via init() (plugin.Factory pattern) and follows the
// same architecture as the sensitive word plugin (internal/plugin/sensitive).
//
// Key constraint: records only semantic fields (BlockedBy="pii", PII type
// count). NEVER logs prompt/message content — ADR-0021 §2 privacy rule.
package pii

import (
	"fmt"
	"regexp"

	"voxeltoad/internal/plugin"
)

const name = "pii"

// Config describes the configuration for the PII plugin.
type Config struct {
	// Enabled controls whether detection is active.
	Enabled bool `json:"enabled,omitempty"`
	// Mode controls behavior: "block" short-circuits the request on detection;
	// "redact" replaces PII with [REDACTED] and allows the request to proceed.
	Mode string `json:"mode,omitempty"` // "block" (default) | "redact"
	// Patterns enable individual PII detectors. When empty, all detectors run.
	Patterns []string `json:"patterns,omitempty"` // "email", "phone", "id_card", "credit_card"
}

// Plugin implements the plugin.Plugin interface for PII detection and redaction.
type Plugin struct {
	mode     string   // "block" or "redact"
	patterns []string // enabled pattern names
	enabled  bool
}

// NewPlugin builds a PII Plugin from a Config. When mode is empty, "block" is
// assumed. Patterns is a whitelist of detector names; when empty, all detectors
// are active.
func NewPlugin(cfg Config) *Plugin {
	mode := cfg.Mode
	if mode == "" {
		mode = "block"
	}
	patterns := cfg.Patterns
	if len(patterns) == 0 {
		patterns = []string{"email", "phone", "id_card", "credit_card"}
	}
	return &Plugin{mode: mode, patterns: patterns, enabled: cfg.Enabled}
}

func (p *Plugin) Name() string           { return name }
func (p *Plugin) Phases() []plugin.Phase { return []plugin.Phase{plugin.PhasePre} }

func (p *Plugin) Execute(c *plugin.Context, phase plugin.Phase) error {
	if !p.enabled || phase != plugin.PhasePre || c.Request == nil {
		return nil
	}
	detected := false
	for i, msg := range c.Request.Messages {
		if msg.Content.Text() == "" {
			continue
		}
		d, redacted := p.scanContent(msg.Content.Text())
		if d != "" {
			detected = true
			if p.mode == "redact" {
				c.Request.Messages[i].Content.SetText(redacted)
			} else {
				c.Stop = true
				c.BlockedBy = name
				return nil
			}
		}
	}
	// In redact mode with detections, mark in metadata for observability.
	if detected && p.mode == "redact" {
		c.BlockedBy = name // metadata only, request not stopped
	}
	return nil
}

// piiDetector matches a known PII pattern in text and returns a redacted
// version. Returns (detected_type, redacted_string). When no PII is found,
// detected_type is empty.
type piiDetector struct {
	name    string
	re      *regexp.Regexp
	replace string
}

var detectors = []piiDetector{
	// Email: user@domain.tld
	{name: "email", re: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`), replace: "[EMAIL]"},
	// CN phone: 13x/15x/18x/19x-xxxx-xxxx or 1xx xxxx xxxx or plain 11 digits
	{name: "phone", re: regexp.MustCompile(`1[3-9]\d[\- ]?\d{4}[\- ]?\d{4}`), replace: "[PHONE]"},
	// CN ID card: 18 digits (6 area + 8 birthday + 3 seq + 1 checksum)
	{name: "id_card", re: regexp.MustCompile(`\d{6}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]`), replace: "[ID_CARD]"},
	// Credit card: 13-19 digit sequences (common major card formats). Note: this
	// is intentionally broad and may produce false positives on numeric IDs; use
	// the patterns config to disable per-deployment.
	{name: "credit_card", re: regexp.MustCompile(`\d[ \-\d]{11,17}\d`), replace: "[CREDIT_CARD]"},
}

var activeDetectors map[string]*piiDetector

func init() {
	activeDetectors = make(map[string]*piiDetector, len(detectors))
	for i := range detectors {
		activeDetectors[detectors[i].name] = &detectors[i]
	}
	plugin.Register(name, func(cfg any) (plugin.Plugin, error) {
		c, ok := cfg.(Config)
		if !ok {
			return nil, fmt.Errorf("plugin %s: expected Config, got %T", name, cfg)
		}
		return NewPlugin(c), nil
	})
}

// scanContent checks text against enabled detectors. Returns the first
// detected PII type and the redacted text (with all PII replaced).
func (p *Plugin) scanContent(text string) (string, string) {
	var firstHit string
	redacted := text
	for _, patName := range p.patterns {
		d, ok := activeDetectors[patName]
		if !ok {
			continue
		}
		if d.re.MatchString(text) {
			if firstHit == "" {
				firstHit = patName
			}
			redacted = d.re.ReplaceAllString(redacted, d.replace)
		}
	}
	// Only return non-empty hit if PII was actually found.
	if firstHit == "" {
		return "", text
	}
	return firstHit, redacted
}
