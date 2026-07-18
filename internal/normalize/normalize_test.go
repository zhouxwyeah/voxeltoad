package normalize_test

import (
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/normalize"
)

func msgs(pairs ...[2]string) []adapter.Message {
	var out []adapter.Message
	for _, p := range pairs {
		out = append(out, adapter.Message{Role: adapter.Role(p[0]), Content: adapter.NewContentText(p[1])})
	}
	return out
}

func TestInjectMaxTokens_WhenMissing(t *testing.T) {
	req := &adapter.UnifiedRequest{Model: "claude-x"}
	out := normalize.Apply(req, normalize.Target{DefaultMaxTokens: 4096})
	if out.MaxTokens == nil || *out.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %v, want 4096", out.MaxTokens)
	}
}

func TestInjectMaxTokens_RespectsExisting(t *testing.T) {
	n := 100
	req := &adapter.UnifiedRequest{Model: "claude-x", MaxTokens: &n}
	out := normalize.Apply(req, normalize.Target{DefaultMaxTokens: 4096})
	if out.MaxTokens == nil || *out.MaxTokens != 100 {
		t.Errorf("MaxTokens = %v, want 100 (existing preserved)", out.MaxTokens)
	}
}

func TestInjectMaxTokens_NoDefault_LeavesNil(t *testing.T) {
	req := &adapter.UnifiedRequest{Model: "gpt-4o"}
	out := normalize.Apply(req, normalize.Target{DefaultMaxTokens: 0})
	if out.MaxTokens != nil {
		t.Errorf("MaxTokens = %v, want nil when no default", out.MaxTokens)
	}
}

func TestMergeConsecutiveSameRole_WhenRequired(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: msgs(
			[2]string{"user", "first"},
			[2]string{"user", "second"},
			[2]string{"assistant", "reply"},
			[2]string{"user", "third"},
		),
	}
	out := normalize.Apply(req, normalize.Target{RequireAlternation: true})
	want := msgs(
		[2]string{"user", "first\n\nsecond"},
		[2]string{"assistant", "reply"},
		[2]string{"user", "third"},
	)
	if len(out.Messages) != len(want) {
		t.Fatalf("got %d messages %+v, want %d", len(out.Messages), out.Messages, len(want))
	}
	for i := range want {
		if out.Messages[i].Role != want[i].Role || out.Messages[i].Content.Text() != want[i].Content.Text() {
			t.Errorf("msg[%d] = %+v, want %+v", i, out.Messages[i], want[i])
		}
	}
}

func TestMergeConsecutive_NotAppliedWhenNotRequired(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: msgs([2]string{"user", "a"}, [2]string{"user", "b"}),
	}
	out := normalize.Apply(req, normalize.Target{RequireAlternation: false})
	if len(out.Messages) != 2 {
		t.Errorf("messages should be untouched, got %d", len(out.Messages))
	}
}

func TestMultiSystem_ConcatenatedToLeading(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: msgs(
			[2]string{"system", "rule1"},
			[2]string{"user", "hi"},
			[2]string{"system", "rule2"},
		),
	}
	out := normalize.Apply(req, normalize.Target{CollapseSystem: true})
	if out.Messages[0].Role != adapter.RoleSystem || out.Messages[0].Content.Text() != "rule1\n\nrule2" {
		t.Errorf("leading system = %+v, want concatenated rule1\\n\\nrule2", out.Messages[0])
	}
	// Only one system message remains, and it's first.
	systems := 0
	for _, m := range out.Messages {
		if m.Role == adapter.RoleSystem {
			systems++
		}
	}
	if systems != 1 {
		t.Errorf("system count = %d, want 1", systems)
	}
	// User message preserved.
	foundUser := false
	for _, m := range out.Messages {
		if m.Role == adapter.RoleUser && m.Content.Text() == "hi" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Error("user message lost")
	}
}

func TestApply_DoesNotMutateInput(t *testing.T) {
	n := 50
	req := &adapter.UnifiedRequest{
		Model:     "m",
		MaxTokens: &n,
		Messages:  msgs([2]string{"user", "a"}, [2]string{"user", "b"}),
	}
	_ = normalize.Apply(req, normalize.Target{RequireAlternation: true, DefaultMaxTokens: 4096})
	if len(req.Messages) != 2 {
		t.Error("Apply must not mutate the input request's messages")
	}
}

// --- Tool message tests (tool_call_id / tool_calls preservation) ---

func TestMergeConsecutive_ToolMessagesNotMerged(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Content: adapter.NewContentText("What is the weather?")},
			{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
				{ID: "call_1", Type: "function", Function: adapter.FunctionCall{Name: "get_weather", Arguments: `{"city":"Beijing"}`}},
			}},
			{Role: adapter.RoleTool, Content: adapter.NewContentText("sunny, 25C"), ToolCallID: "call_1"},
			{Role: adapter.RoleTool, Content: adapter.NewContentText("windy, 18C"), ToolCallID: "call_2"},
		},
	}
	out := normalize.Apply(req, normalize.Target{RequireAlternation: true})

	// Tool messages must not be merged.
	if len(out.Messages) != 4 {
		t.Fatalf("got %d messages, want 4 (tool messages must not be merged)", len(out.Messages))
	}
	if out.Messages[2].ToolCallID != "call_1" {
		t.Errorf("msg[2].ToolCallID = %q, want call_1", out.Messages[2].ToolCallID)
	}
	if out.Messages[3].ToolCallID != "call_2" {
		t.Errorf("msg[3].ToolCallID = %q, want call_2", out.Messages[3].ToolCallID)
	}
}

func TestMergeConsecutive_AssistantWithToolCallsNotMerged(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: []adapter.Message{
			{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")},
			{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
				{ID: "call_a", Type: "function", Function: adapter.FunctionCall{Name: "f", Arguments: "{}"}},
			}},
			{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
				{ID: "call_b", Type: "function", Function: adapter.FunctionCall{Name: "g", Arguments: "{}"}},
			}},
		},
	}
	out := normalize.Apply(req, normalize.Target{RequireAlternation: true})

	// Consecutive assistant messages with tool_calls must not be merged.
	if len(out.Messages) != 3 {
		t.Fatalf("got %d messages, want 3 (assistant with tool_calls not merged)", len(out.Messages))
	}
	if out.Messages[1].ToolCalls[0].ID != "call_a" {
		t.Errorf("msg[1].ToolCalls[0].ID = %q, want call_a", out.Messages[1].ToolCalls[0].ID)
	}
	if out.Messages[2].ToolCalls[0].ID != "call_b" {
		t.Errorf("msg[2].ToolCalls[0].ID = %q, want call_b", out.Messages[2].ToolCalls[0].ID)
	}
}

func TestMergeConsecutive_PlainAssistantStillMerged(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: []adapter.Message{
			{Role: adapter.RoleAssistant, Content: adapter.NewContentText("Hello")},
			{Role: adapter.RoleAssistant, Content: adapter.NewContentText("World")},
		},
	}
	out := normalize.Apply(req, normalize.Target{RequireAlternation: true})

	// Plain assistant messages (no tool_calls) should still be merged.
	if len(out.Messages) != 1 {
		t.Fatalf("got %d messages, want 1 (plain assistant should merge)", len(out.Messages))
	}
	if out.Messages[0].Content.Text() != "Hello\n\nWorld" {
		t.Errorf("Content = %q, want Hello\\n\\nWorld", out.Messages[0].Content)
	}
}

func TestCloneMessages_DeepCopiesToolCalls(t *testing.T) {
	orig := []adapter.Message{
		{Role: adapter.RoleAssistant, ToolCalls: []adapter.ToolCall{
			{ID: "c1", Type: "function", Function: adapter.FunctionCall{Name: "f", Arguments: "{}"}},
		}},
	}

	// Use Apply which calls cloneMessages internally.
	req := &adapter.UnifiedRequest{Messages: orig}
	out := normalize.Apply(req, normalize.Target{})

	// Modify the clone's ToolCalls — original must be unaffected.
	out.Messages[0].ToolCalls[0].ID = "hacked"
	if orig[0].ToolCalls[0].ID != "c1" {
		t.Errorf("original ToolCalls[0].ID was mutated: got %q, want c1", orig[0].ToolCalls[0].ID)
	}
}

func TestApply_PreservesToolCallFields_FullPipeline(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Model: "gpt-4o",
		Messages: []adapter.Message{
			{Role: adapter.RoleSystem, Content: adapter.NewContentText("You are helpful.")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("What is the weather in Beijing?")},
			{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
				{ID: "call_w", Type: "function", Function: adapter.FunctionCall{Name: "get_weather", Arguments: `{"city":"Beijing"}`}},
			}},
			{Role: adapter.RoleTool, Content: adapter.NewContentText(`{"temp":25}`), ToolCallID: "call_w"},
			{Role: adapter.RoleAssistant, Content: adapter.NewContentText("Beijing is sunny, 25°C.")},
		},
	}

	// OpenAI target: no collapse, no alternation — messages should pass through untouched.
	out := normalize.Apply(req, normalize.Target{})

	if len(out.Messages) != 5 {
		t.Fatalf("got %d messages, want 5", len(out.Messages))
	}

	// Assistant message with tool_calls.
	assist := out.Messages[2]
	if assist.Role != adapter.RoleAssistant {
		t.Errorf("msg[2].Role = %q, want assistant", assist.Role)
	}
	if len(assist.ToolCalls) != 1 {
		t.Fatalf("msg[2].ToolCalls len = %d, want 1", len(assist.ToolCalls))
	}
	if assist.ToolCalls[0].ID != "call_w" {
		t.Errorf("msg[2].ToolCalls[0].ID = %q, want call_w", assist.ToolCalls[0].ID)
	}
	if assist.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("msg[2].ToolCalls[0].Function.Name = %q, want get_weather", assist.ToolCalls[0].Function.Name)
	}
	if assist.ToolCalls[0].Function.Arguments != `{"city":"Beijing"}` {
		t.Errorf("msg[2].ToolCalls[0].Function.Arguments = %q", assist.ToolCalls[0].Function.Arguments)
	}

	// Tool message with tool_call_id.
	toolMsg := out.Messages[3]
	if toolMsg.Role != adapter.RoleTool {
		t.Errorf("msg[3].Role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_w" {
		t.Errorf("msg[3].ToolCallID = %q, want call_w", toolMsg.ToolCallID)
	}
	if toolMsg.Content.Text() != `{"temp":25}` {
		t.Errorf("msg[3].Content = %q, want {\"temp\":25}", toolMsg.Content)
	}

	// Final assistant reply.
	if out.Messages[4].Content.Text() != "Beijing is sunny, 25°C." {
		t.Errorf("msg[4].Content = %q", out.Messages[4].Content)
	}
}

func TestApply_ClaudeTarget_PreservesToolMessages(t *testing.T) {
	// Claude target: CollapseSystem + RequireAlternation.
	// Tool messages should not be merged even under RequireAlternation.
	req := &adapter.UnifiedRequest{
		Messages: []adapter.Message{
			{Role: adapter.RoleSystem, Content: adapter.NewContentText("sys")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("u1")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("u2")},
			{Role: adapter.RoleAssistant, Content: adapter.Content{}, ToolCalls: []adapter.ToolCall{
				{ID: "c1", Type: "function", Function: adapter.FunctionCall{Name: "f", Arguments: "{}"}},
			}},
			{Role: adapter.RoleTool, Content: adapter.NewContentText("r1"), ToolCallID: "c1"},
			{Role: adapter.RoleTool, Content: adapter.NewContentText("r2"), ToolCallID: "c2"},
		},
	}
	out := normalize.Apply(req, normalize.Target{
		CollapseSystem:     true,
		RequireAlternation: true,
		DefaultMaxTokens:   2048,
	})

	// System should be collapsed to one leading message.
	// User messages should be merged.
	// Assistant with tool_calls stays separate.
	// Tool messages stay separate.
	// Expected: system, user(merged), assistant(tool_calls), tool(c1), tool(c2)
	if len(out.Messages) != 5 {
		t.Fatalf("got %d messages, want 5", len(out.Messages))
	}
	if out.Messages[0].Role != adapter.RoleSystem {
		t.Errorf("msg[0].Role = %q, want system", out.Messages[0].Role)
	}
	if out.Messages[1].Role != adapter.RoleUser || out.Messages[1].Content.Text() != "u1\n\nu2" {
		t.Errorf("msg[1] = %+v, want user merged", out.Messages[1])
	}
	if out.Messages[2].Role != adapter.RoleAssistant || len(out.Messages[2].ToolCalls) != 1 {
		t.Errorf("msg[2] = %+v, want assistant with tool_calls", out.Messages[2])
	}
	if out.Messages[3].Role != adapter.RoleTool || out.Messages[3].ToolCallID != "c1" {
		t.Errorf("msg[3] = %+v, want tool c1", out.Messages[3])
	}
	if out.Messages[4].Role != adapter.RoleTool || out.Messages[4].ToolCallID != "c2" {
		t.Errorf("msg[4] = %+v, want tool c2", out.Messages[4])
	}
}

// CollapseSystem + RequireAlternation together (the Claude target) must yield a
// single leading system followed by strictly alternating user/assistant.
func TestApply_ClaudeTarget(t *testing.T) {
	req := &adapter.UnifiedRequest{
		Messages: msgs(
			[2]string{"system", "sys"},
			[2]string{"user", "u1"},
			[2]string{"user", "u2"},
			[2]string{"assistant", "a1"},
		),
	}
	out := normalize.Apply(req, normalize.Target{
		CollapseSystem:     true,
		RequireAlternation: true,
		DefaultMaxTokens:   2048,
	})
	want := msgs(
		[2]string{"system", "sys"},
		[2]string{"user", "u1\n\nu2"},
		[2]string{"assistant", "a1"},
	)
	if len(out.Messages) != len(want) {
		t.Fatalf("got %d %+v, want %d", len(out.Messages), out.Messages, len(want))
	}
	for i := range want {
		if out.Messages[i].Role != want[i].Role || out.Messages[i].Content.Text() != want[i].Content.Text() {
			t.Errorf("msg[%d] = %+v, want %+v", i, out.Messages[i], want[i])
		}
	}
	if out.MaxTokens == nil || *out.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %v, want 2048", out.MaxTokens)
	}
}
