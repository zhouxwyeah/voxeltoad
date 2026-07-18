package proxy

import (
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

func prepDynamic() *config.Dynamic {
	return &config.Dynamic{
		Providers: []config.Provider{
			{Name: "openai-prod", Adapter: "openai"},
			{Name: "anthropic", Adapter: "claude"},
		},
		Models: []config.Model{{
			Alias: "default-chat",
			Upstreams: []config.ModelUpstream{
				{Provider: "openai-prod", UpstreamModel: "gpt-4o", DefaultMaxTokens: 0},
				{Provider: "anthropic", UpstreamModel: "claude-3-5-sonnet", DefaultMaxTokens: 1024},
			},
		}},
	}
}

func TestPreparer_ResolvesUpstreamModelPerProvider(t *testing.T) {
	p := newModelPreparer(prepDynamic())

	got, err := p.Prepare(&adapter.UnifiedRequest{Model: "default-chat"}, "openai-prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-4o" {
		t.Errorf("openai upstream model = %q, want gpt-4o", got.Model)
	}

	got, err = p.Prepare(&adapter.UnifiedRequest{Model: "default-chat"}, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-3-5-sonnet" {
		t.Errorf("claude upstream model = %q, want claude-3-5-sonnet", got.Model)
	}
}

func TestPreparer_ClaudeTargetNormalizes(t *testing.T) {
	p := newModelPreparer(prepDynamic())
	req := &adapter.UnifiedRequest{
		Model: "default-chat",
		Messages: []adapter.Message{
			{Role: adapter.RoleSystem, Content: adapter.NewContentText("sys")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("u1")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("u2")},
		},
	}
	got, err := p.Prepare(req, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	// Claude target: max_tokens default injected (1024), consecutive users merged.
	if got.MaxTokens == nil || *got.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %v, want 1024 (Claude default)", got.MaxTokens)
	}
	// system stays leading + single; two users merged into one.
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d %+v, want 2 (system + merged user)", len(got.Messages), got.Messages)
	}
	if got.Messages[1].Role != adapter.RoleUser || got.Messages[1].Content.Text() != "u1\n\nu2" {
		t.Errorf("merged user = %+v, want u1\\n\\nu2", got.Messages[1])
	}
}

func TestPreparer_OpenAITargetDoesNotMergeOrDefault(t *testing.T) {
	p := newModelPreparer(prepDynamic())
	req := &adapter.UnifiedRequest{
		Model: "default-chat",
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Content: adapter.NewContentText("u1")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("u2")},
		},
	}
	got, err := p.Prepare(req, "openai-prod")
	if err != nil {
		t.Fatal(err)
	}
	// OpenAI: no default max_tokens (upstream default 0), consecutive users kept.
	if got.MaxTokens != nil {
		t.Errorf("MaxTokens = %v, want nil for OpenAI (no default)", got.MaxTokens)
	}
	if len(got.Messages) != 2 {
		t.Errorf("messages = %d, want 2 unchanged (OpenAI allows consecutive)", len(got.Messages))
	}
}

func TestPreparer_UnmappedProviderErrors(t *testing.T) {
	p := newModelPreparer(prepDynamic())
	if _, err := p.Prepare(&adapter.UnifiedRequest{Model: "default-chat"}, "zhipu"); err == nil {
		t.Error("provider not serving the alias should error")
	}
}

func TestPreparer_DoesNotMutateInput(t *testing.T) {
	p := newModelPreparer(prepDynamic())
	req := &adapter.UnifiedRequest{Model: "default-chat", Messages: []adapter.Message{
		{Role: adapter.RoleUser, Content: adapter.NewContentText("u1")}, {Role: adapter.RoleUser, Content: adapter.NewContentText("u2")},
	}}
	_, _ = p.Prepare(req, "anthropic")
	if req.Model != "default-chat" || len(req.Messages) != 2 {
		t.Error("Prepare must not mutate the input request")
	}
}
