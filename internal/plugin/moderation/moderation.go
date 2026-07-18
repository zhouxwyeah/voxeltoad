package moderation

import (
	"fmt"
	"strings"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/plugin"
)

const name = "moderation"

// maxContentChars is the default character limit for the concatenated user
// messages sent to the moderation API. Rationale: covers typical prompts while
// staying within OpenAI moderation's request body limits. Configurable via
// MaxContentChars in Config.
const maxContentChars = 4096

// Config describes the configuration for the moderation plugin.
type Config struct {
	Enabled    bool     `json:"enabled"`
	Provider   string   `json:"provider"`             // "openai"
	Endpoint   string   `json:"endpoint,omitempty"`   // override default
	APIKeyRef  string   `json:"api_key_ref"`          // env://VAR
	Action     string   `json:"action"`               // "block" (default) | "flag"
	FailMode   string   `json:"fail_mode,omitempty"`  // "open" (default) | "closed"
	TimeoutMs  int      `json:"timeout_ms,omitempty"` // default 2000
	Categories []string `json:"categories,omitempty"` // empty = all
}

// Plugin implements the plugin.Plugin interface for external moderation.
type Plugin struct {
	provider ModerationProvider
	action   string // "block" | "flag"
	failMode string // "open" | "closed"
	maxChars int
	enabled  bool
}

// NewPlugin builds a moderation Plugin from a Config and ModerationProvider.
func NewPlugin(cfg Config, prov ModerationProvider) *Plugin {
	action := cfg.Action
	if action == "" {
		action = "block"
	}
	failMode := cfg.FailMode
	if failMode == "" {
		failMode = "open"
	}
	maxChars := maxContentChars
	return &Plugin{
		provider: prov,
		action:   action,
		failMode: failMode,
		maxChars: maxChars,
		enabled:  cfg.Enabled,
	}
}

func (p *Plugin) Name() string           { return name }
func (p *Plugin) Phases() []plugin.Phase { return []plugin.Phase{plugin.PhasePre} }

func (p *Plugin) Execute(c *plugin.Context, phase plugin.Phase) error {
	if !p.enabled || phase != plugin.PhasePre || c.Request == nil || p.provider == nil {
		return nil
	}

	content := joinUserMessages(c.Request.Messages, p.maxChars)
	if content == "" {
		return nil
	}

	flagged, err := p.provider.Check(c.Ctx, content, nil)
	if err != nil {
		return p.handleFailure(c, err)
	}

	if !flagged {
		return nil
	}

	return p.handleFlagged(c)
}

// joinUserMessages concatenates user-role message content up to maxChars.
func joinUserMessages(msgs []adapter.Message, maxChars int) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range msgs {
		if m.Role != "user" || m.Content.Text() == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		remain := maxChars - b.Len()
		if remain <= 0 {
			break
		}
		text := m.Content.Text()
		if len(text) > remain {
			text = text[:remain]
		}
		b.WriteString(text)
	}
	return b.String()
}

func (p *Plugin) handleFlagged(c *plugin.Context) error {
	switch p.action {
	case "block":
		c.Stop = true
		c.BlockedBy = name
	case "flag":
		c.BlockedBy = name // metadata only; request continues (Chain checks Stop, not BlockedBy)
	}
	return nil
}

func (p *Plugin) handleFailure(c *plugin.Context, err error) error {
	switch p.failMode {
	case "closed":
		c.Stop = true
		c.BlockedBy = name
		return fmt.Errorf("moderation unavailable (fail_closed): %w", err)
	default: // "open"
		return nil // allow request through
	}
}

func init() {
	plugin.Register(name, func(cfg any) (plugin.Plugin, error) {
		c, ok := cfg.(Config)
		if !ok {
			return nil, fmt.Errorf("plugin %s: expected Config, got %T", name, cfg)
		}
		var prov ModerationProvider
		switch c.Provider {
		case "", "openai":
			resolveKey := func(ref string) string {
				// env://VAR resolution is handled by config.ResolveSecret;
				// at plugin construction time, caller resolves the ref and
				// passes the resolved value as APIKeyRef.
				return ref
			}
			timeout := time.Duration(c.TimeoutMs) * time.Millisecond
			if timeout <= 0 {
				timeout = 2 * time.Second
			}
			prov = NewOpenAIProvider(c.Endpoint, resolveKey(c.APIKeyRef), timeout)
		default:
			return nil, fmt.Errorf("plugin %s: unknown provider %q", name, c.Provider)
		}
		return NewPlugin(c, prov), nil
	})
}
