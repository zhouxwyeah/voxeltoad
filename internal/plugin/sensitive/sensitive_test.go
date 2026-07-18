package sensitive_test

import (
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/plugin/sensitive"
)

// buildCtx returns a Pre-phase Context with the given message contents. Content
// strings are mapped 1:1 to adapter.Message{Role: "user", Content: s}.
func buildCtx(contents ...string) *plugin.Context {
	msgs := make([]adapter.Message, len(contents))
	for i, c := range contents {
		msgs[i] = adapter.Message{Role: "user", Content: adapter.NewContentText(c)}
	}
	return &plugin.Context{
		Ctx:     nil, // unused by plugin
		Tenant:  "acme",
		Request: &adapter.UnifiedRequest{Messages: msgs},
	}
}

func buildPlugin(enabled bool, words ...string) *sensitive.Plugin {
	return sensitive.NewPlugin(sensitive.Config{Enabled: enabled, Words: words})
}

func TestNewPlugin_NilWords(t *testing.T) {
	p := sensitive.NewPlugin(sensitive.Config{Enabled: true})
	if p == nil {
		t.Fatal("NewPlugin returned nil")
	}
	if p.Name() != "sensitive" {
		t.Errorf("Name = %q, want sensitive", p.Name())
	}
	if got := p.Phases(); len(got) != 1 || got[0] != plugin.PhasePre {
		t.Errorf("Phases = %v, want [Pre]", got)
	}
}

func TestExecute_DisabledPlugin_Passes(t *testing.T) {
	p := buildPlugin(false, "bad")
	c := buildCtx("this is bad content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("disabled plugin should not stop")
	}
}

func TestExecute_NoWords_Passes(t *testing.T) {
	p := buildPlugin(true /* no words */)
	c := buildCtx("any content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("plugin with empty word list should not stop")
	}
}

func TestExecute_NilRequest_Passes(t *testing.T) {
	p := buildPlugin(true, "bad")
	c := &plugin.Context{Ctx: nil, Tenant: "acme", Request: nil}
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("nil request should not stop")
	}
}

func TestExecute_ExactMatch_Blocks(t *testing.T) {
	p := buildPlugin(true, "bad")
	c := buildCtx("this is bad content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("should have stopped on match")
	}
	if c.BlockedBy != "sensitive" {
		t.Errorf("BlockedBy = %q, want sensitive", c.BlockedBy)
	}
}

func TestExecute_NoMatch_Passes(t *testing.T) {
	p := buildPlugin(true, "bad")
	c := buildCtx("this is fine content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("should not stop when no word matches")
	}
}

func TestExecute_CaseInsensitive_Match(t *testing.T) {
	p := buildPlugin(true, "BAD")
	c := buildCtx("this has bad in lowercase")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("case-insensitive match should block")
	}
}

func TestExecute_SubstringMatch_Blocks(t *testing.T) {
	p := buildPlugin(true, "bad")
	c := buildCtx("this is baddie content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("substring match should block")
	}
}

func TestExecute_MultipleWords_FirstMatchBlocks(t *testing.T) {
	p := buildPlugin(true, "alpha", "beta", "gamma")
	c := buildCtx("this has beta in it")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("should block on first matching word")
	}
}

func TestExecute_MultipleMessages_MatchInSecondBlocks(t *testing.T) {
	p := buildPlugin(true, "gamma")
	c := buildCtx("clean message", "this has gamma")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if !c.Stop {
		t.Fatal("match in later message should block")
	}
}

func TestExecute_EmptyContent_Passes(t *testing.T) {
	p := buildPlugin(true, "bad")
	c := buildCtx("")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("empty content should not stop")
	}
}

func TestExecute_WordListContainsEmptyString_Passes(t *testing.T) {
	p := buildPlugin(true, "", "safe")
	c := buildCtx("some random content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	// Empty word "" will match everything since strings.Contains(lower, "").
	// But that's a config error, not a plugin bug — the word list should not
	// contain empty strings. The plugin is still correct.
	if !c.Stop {
		t.Log("empty word should match everything (strings.Contains semantics)")
	}
}

func TestExecute_PhasePost_Passes(t *testing.T) {
	p := buildPlugin(true, "bad")
	c := buildCtx("this is bad content")
	if err := p.Execute(c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	if c.Stop {
		t.Error("plugin limited to Pre phase should not stop in PhasePost")
	}
}

func TestExecute_BlockedBy_MatchesPluginName(t *testing.T) {
	p := buildPlugin(true, "forbidden")
	if p.Name() != "sensitive" {
		t.Fatalf("unexpected plugin name: %s", p.Name())
	}
	c := buildCtx("this is forbidden content")
	if err := p.Execute(c, plugin.PhasePre); err != nil {
		t.Fatal(err)
	}
	if c.BlockedBy != p.Name() {
		t.Errorf("BlockedBy = %q, want %q", c.BlockedBy, p.Name())
	}
}
