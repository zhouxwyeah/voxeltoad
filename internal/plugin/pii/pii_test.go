package pii_test

import (
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/plugin/pii"
)

func buildCtx(contents ...string) *plugin.Context {
	msgs := make([]adapter.Message, len(contents))
	for i, c := range contents {
		msgs[i] = adapter.Message{Role: "user", Content: adapter.NewContentText(c)}
	}
	return &plugin.Context{
		Tenant:  "acme",
		Request: &adapter.UnifiedRequest{Messages: msgs},
	}
}

func buildPlugin(mode string, patterns ...string) *pii.Plugin {
	return pii.NewPlugin(pii.Config{Enabled: true, Mode: mode, Patterns: patterns})
}

func TestBlock_Email_Blocks(t *testing.T) {
	p := buildPlugin("block", "email")
	c := buildCtx("contact us at user@example.com please")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("should block on email")
	}
	if c.BlockedBy != "pii" {
		t.Errorf("BlockedBy = %q, want pii", c.BlockedBy)
	}
}

func TestBlock_Phone_Blocks(t *testing.T) {
	p := buildPlugin("block", "phone")
	c := buildCtx("call 13800138000 for support")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("should block on phone")
	}
}

func TestBlock_IDCard_Blocks(t *testing.T) {
	p := buildPlugin("block", "id_card")
	c := buildCtx("my id is 110101199003071234")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("should block on ID card")
	}
}

func TestBlock_NoPII_Passes(t *testing.T) {
	p := buildPlugin("block")
	c := buildCtx("this is a normal message")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Fatal("should not block clean message")
	}
}

func TestRedact_Email_Redacts(t *testing.T) {
	p := buildPlugin("redact", "email")
	c := buildCtx("hi from user@example.com")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("redact mode should not stop")
	}
	if c.BlockedBy != "pii" {
		t.Errorf("BlockedBy = %q, want pii (metadata)", c.BlockedBy)
	}
	redacted := c.Request.Messages[0].Content.Text()
	if redacted == "hi from user@example.com" {
		t.Error("content was not redacted")
	}
}

func TestRedact_Phone_Hyphen(t *testing.T) {
	p := buildPlugin("redact", "phone")
	c := buildCtx("call 138-0013-8000 now")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	redacted := c.Request.Messages[0].Content.Text()
	if redacted == "call 138-0013-8000 now" {
		t.Error("phone was not redacted")
	}
}

func TestDisabledDoNothing(t *testing.T) {
	p := pii.NewPlugin(pii.Config{Enabled: false})
	c := buildCtx("my email is user@example.com")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("disabled plugin should not stop")
	}
}

func TestEmptyMessageDoNothing(t *testing.T) {
	p := buildPlugin("block")
	c := buildCtx("")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("empty message should not stop")
	}
}

func TestPhasePostDoNothing(t *testing.T) {
	p := buildPlugin("block")
	c := buildCtx("user@example.com")
	if err := p.Execute(c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("should not act in PhasePost")
	}
}
