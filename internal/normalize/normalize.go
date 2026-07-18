// Package normalize adapts a valid OpenAI-style UnifiedRequest into a form the
// target provider accepts, WITHOUT burdening adapters with semantic rewriting
// (see ADR-0009). It runs after routing (target is known) and before the
// adapter's BuildRequest. Adapters remain pure translators.
//
// Normalization is provider-aware via Target flags: an OpenAI target needs
// none of the Claude-specific rewrites, so it leaves the request essentially
// untouched (aside from a max_tokens default if configured).
package normalize

import (
	"strings"

	"voxeltoad/internal/adapter"
)

// Target describes what the resolved provider/model requires.
type Target struct {
	// DefaultMaxTokens, if > 0, is injected when the request omits max_tokens
	// (required by Claude).
	DefaultMaxTokens int
	// CollapseSystem merges all system-role messages into a single leading
	// system message (Claude has one top-level system field).
	CollapseSystem bool
	// RequireAlternation merges consecutive same-role turns so messages strictly
	// alternate (Claude rejects consecutive same-role messages).
	RequireAlternation bool
}

// Apply returns a normalized copy of req. It never mutates the input.
func Apply(req *adapter.UnifiedRequest, t Target) *adapter.UnifiedRequest {
	out := *req // shallow copy; Messages/MaxTokens handled below

	// max_tokens default.
	if out.MaxTokens == nil && t.DefaultMaxTokens > 0 {
		n := t.DefaultMaxTokens
		out.MaxTokens = &n
	}

	msgs := cloneMessages(req.Messages)
	if t.CollapseSystem {
		msgs = collapseSystem(msgs)
	}
	if t.RequireAlternation {
		msgs = mergeConsecutive(msgs)
	}
	out.Messages = msgs
	return &out
}

func cloneMessages(in []adapter.Message) []adapter.Message {
	out := make([]adapter.Message, len(in))
	for i, m := range in {
		out[i] = m
		if m.ToolCalls != nil {
			out[i].ToolCalls = make([]adapter.ToolCall, len(m.ToolCalls))
			copy(out[i].ToolCalls, m.ToolCalls)
		}
	}
	return out
}

// collapseSystem concatenates all system messages (in order) into one leading
// system message, preserving the order of non-system messages after it.
func collapseSystem(in []adapter.Message) []adapter.Message {
	var systems []string
	rest := make([]adapter.Message, 0, len(in))
	for _, m := range in {
		if m.Role == adapter.RoleSystem {
			systems = append(systems, m.Content.Text())
			continue
		}
		rest = append(rest, m)
	}
	if len(systems) == 0 {
		return in
	}
	out := make([]adapter.Message, 0, len(rest)+1)
	out = append(out, adapter.Message{Role: adapter.RoleSystem, Content: adapter.NewContentText(strings.Join(systems, "\n\n"))})
	out = append(out, rest...)
	return out
}

// mergeConsecutive merges adjacent messages with the same role, joining their
// content with "\n\n", producing a strictly alternating sequence.
//
// Tool messages are never merged — each carries a unique tool_call_id that must
// be preserved. Assistant messages that contain tool_calls are also never merged,
// as merging would corrupt the tool call history.
func mergeConsecutive(in []adapter.Message) []adapter.Message {
	if len(in) == 0 {
		return in
	}
	out := make([]adapter.Message, 0, len(in))
	for _, m := range in {
		if len(out) > 0 && out[len(out)-1].Role == m.Role {
			// Never merge tool messages (each has a unique tool_call_id).
			if m.Role == adapter.RoleTool {
				out = append(out, m)
				continue
			}
			// Never merge assistant messages that carry tool_calls on either side.
			if m.Role == adapter.RoleAssistant && (len(m.ToolCalls) > 0 || len(out[len(out)-1].ToolCalls) > 0) {
				out = append(out, m)
				continue
			}
			out[len(out)-1].Content.SetText(out[len(out)-1].Content.Text() + "\n\n" + m.Content.Text())
			continue
		}
		out = append(out, m)
	}
	return out
}
