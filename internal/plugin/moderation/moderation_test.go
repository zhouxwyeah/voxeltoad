package moderation_test

import (
	"context"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/plugin/moderation"
)

// stubProvider is a controllable ModerationProvider for tests.
type stubProvider struct {
	flagged bool
	err     error
}

func (s *stubProvider) Check(_ context.Context, _ string, _ []string) (bool, error) {
	return s.flagged, s.err
}

func buildCtx(contents ...string) *plugin.Context {
	msgs := make([]adapter.Message, len(contents))
	for i, c := range contents {
		msgs[i] = adapter.Message{Role: "user", Content: adapter.NewContentText(c)}
	}
	return &plugin.Context{
		Ctx:     context.Background(),
		Tenant:  "acme",
		Request: &adapter.UnifiedRequest{Messages: msgs},
	}
}

func buildPlugin(action, failMode string, flagged bool, err error) *moderation.Plugin {
	return moderation.NewPlugin(moderation.Config{
		Enabled:  true,
		Provider: "openai",
		Action:   action,
		FailMode: failMode,
	}, &stubProvider{flagged: flagged, err: err})
}

func TestBlock_Flagged_Stops(t *testing.T) {
	p := buildPlugin("block", "open", true, nil)
	c := buildCtx("some flagged content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("block mode should stop on flagged content")
	}
	if c.BlockedBy != "moderation" {
		t.Errorf("BlockedBy = %q, want moderation", c.BlockedBy)
	}
}

func TestBlock_Clean_Passes(t *testing.T) {
	p := buildPlugin("block", "open", false, nil)
	c := buildCtx("clean content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatal("should not stop on clean content")
	}
}

func TestFlag_Flagged_Continues(t *testing.T) {
	p := buildPlugin("flag", "open", true, nil)
	c := buildCtx("flagged content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatal("flag mode should not stop")
	}
	if c.BlockedBy != "moderation" {
		t.Errorf("BlockedBy = %q, want moderation (metadata)", c.BlockedBy)
	}
}

func TestFailOpen_Error_Allows(t *testing.T) {
	p := buildPlugin("block", "open", false, context.DeadlineExceeded)
	c := buildCtx("content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatal("fail_open should allow request on error")
	}
}

func TestFailClosed_Error_Blocks(t *testing.T) {
	p := buildPlugin("block", "closed", false, context.DeadlineExceeded)
	c := buildCtx("content")
	err := p.Execute(c, plugin.PhasePre)
	if err == nil {
		t.Fatal("fail_closed should return error")
	}
	if !c.Stop {
		t.Fatal("fail_closed should stop on error")
	}
}

func TestDisabledDoNothing(t *testing.T) {
	p := moderation.NewPlugin(moderation.Config{Enabled: false, Provider: "openai"}, nil)
	c := buildCtx("flagged content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("disabled plugin should not stop")
	}
}

func TestNilProviderDoNothing(t *testing.T) {
	p := moderation.NewPlugin(moderation.Config{Enabled: true, Provider: "openai"}, nil)
	c := buildCtx("content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("nil provider should not stop")
	}
}

func TestEmptyMessagesDoNothing(t *testing.T) {
	p := buildPlugin("block", "open", true, nil)
	c := buildCtx("")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("empty message should not stop")
	}
}

func TestSystemMessagesSkipped(t *testing.T) {
	p := buildPlugin("block", "open", true, nil)
	c := &plugin.Context{
		Ctx:    context.Background(),
		Tenant: "acme",
		Request: &adapter.UnifiedRequest{
			Messages: []adapter.Message{
				{Role: "system", Content: adapter.NewContentText("you are helpful")},
			},
		},
	}
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatal("system messages should be skipped, not blocked")
	}
}

func TestPhasePostDoNothing(t *testing.T) {
	p := buildPlugin("block", "open", true, nil)
	c := buildCtx("flagged")
	if err := p.Execute(c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("moderation is Pre-phase only")
	}
}

func TestContentTruncation(t *testing.T) {
	longText := strings.Repeat("a", 5000)
	p := buildPlugin("block", "open", false, nil)
	c := buildCtx(longText)
	// Should not panic; check is called on truncated content.
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProvider_Builds(t *testing.T) {
	prov := moderation.NewOpenAIProvider("", "", 0)
	if prov == nil {
		t.Fatal("NewOpenAIProvider returned nil")
	}
}
