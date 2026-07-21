// Package anthropic implements the ingress Codec for the Anthropic Messages
// API wire format (/v1/messages). It translates Anthropic client requests to
// the unified (OpenAI-compatible) model and unified responses back to the
// Anthropic shape — so Claude Code (which only speaks Anthropic) can drive
// OpenAI-protocol upstreams through the gateway.
//
// Scope (ADR-0045): non-streaming + streaming + tool-use mapping are fully
// supported for the text + tool payload Claude Code actually generates. Image
// content blocks are not supported in the first version (they fail decode with
// a clear error). cache_control is dropped (OpenAI upstreams don't honor it).
package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/ingress"
)

func init() {
	ingress.Register(&codec{})
}

// codec is the Anthropic ingress codec. Stateless apart from the registry
// registration; NewStreamEncoder produces a fresh per-stream state machine.
type codec struct{}

func (*codec) Protocol() ingress.Protocol { return ingress.ProtocolAnthropic }

func (*codec) StreamContentType() string { return "text/event-stream" }

// StreamTerminator returns the Anthropic stream terminator bytes: a
// `message_stop` event (Anthropic has no [DONE] sentinel; the encoder has
// already emitted the terminal message_delta before Close is called).
func (*codec) StreamTerminator() []byte {
	return encodeSSE("message_stop", map[string]any{})
}

// ---- wire types (Anthropic → unified) -----------------------------------

// wireRequest is the subset of Anthropic's /v1/messages request the decoder
// must read to produce a unified request. Unknown fields are ignored silently
// (cache_control, metadata, top_p, …) — see ADR-0045 §gap for cache_control.
type wireRequest struct {
	Model      string          `json:"model"`
	System     json.RawMessage `json:"system,omitempty"` // string OR []content
	Messages   []wireMessage   `json:"messages"`
	MaxTokens  int             `json:"max_tokens"`
	Stream     bool            `json:"stream,omitempty"`
	Temp       *float64        `json:"temperature,omitempty"`
	Tools      []wireTool      `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"` // {type:...} — mapped later
}

// wireMessage is an Anthropic message: role + content. Content may be a plain
// string or an array of typed content blocks; we accept both by reading raw.
type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// wireTool is an Anthropic tool definition: name + description + input_schema
// (a JSON schema for the function's arguments).
type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// wireContentBlock is one element of a content array. Only "text" (user/
// assistant), "tool_use" (assistant), and "tool_result" (user) are handled.
// Other types (e.g. "image") currently cause a decode error.
type wireContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	// Content for tool_result may be a string or a content-block array; we
	// accept both by reading raw.
	Content json.RawMessage `json:"content,omitempty"`
}

// ---- DecodeRequest ------------------------------------------------------

func (c *codec) DecodeRequest(body []byte) (*adapter.UnifiedRequest, error) {
	var wr wireRequest
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("anthropic: parse request: %w", err)
	}

	req := &adapter.UnifiedRequest{
		Model:       wr.Model,
		Stream:      wr.Stream,
		Temperature: wr.Temp,
	}
	if wr.MaxTokens > 0 {
		mt := wr.MaxTokens
		req.MaxTokens = &mt
	}

	// Top-level system → leading unified system message (string or array).
	if len(wr.System) > 0 {
		sysText, err := decodeSystem(wr.System)
		if err != nil {
			return nil, err
		}
		if sysText != "" {
			req.Messages = append(req.Messages, adapter.Message{
				Role:    adapter.RoleSystem,
				Content: adapter.NewContentText(sysText),
			})
		}
	}

	// Tools: input_schema → function.parameters (carried verbatim).
	if len(wr.Tools) > 0 {
		req.Tools = make([]adapter.Tool, len(wr.Tools))
		for i, t := range wr.Tools {
			req.Tools[i] = adapter.Tool{
				Type: "function",
				Function: adapter.FunctionDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  append([]byte(nil), t.InputSchema...),
				},
			}
		}
	}

	// Messages: each Anthropic message maps to one or more unified messages
	// (a user message with mixed text + tool_result blocks is split).
	for _, m := range wr.Messages {
		expanded, err := decodeMessage(m)
		if err != nil {
			return nil, err
		}
		req.Messages = append(req.Messages, expanded...)
	}

	// tool_choice: optional. Mapped when present.
	if len(wr.ToolChoice) > 0 {
		req.ToolChoice = decodeToolChoice(wr.ToolChoice)
	}

	return req, nil
}

// decodeSystem accepts the Anthropic system field in either of its two forms
// (string or array of {type:text,...} blocks) and returns the concatenated
// plain text. Blocks are joined with "\n\n" (matching how the normalization
// layer joins multiple OpenAI system messages).
func decodeSystem(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	// String form.
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("anthropic: parse system string: %w", err)
		}
		return s, nil
	}
	// Array form: concatenate text blocks.
	if trimmed[0] == '[' {
		var blocks []wireContentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", fmt.Errorf("anthropic: parse system array: %w", err)
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n\n"), nil
	}
	return "", fmt.Errorf("anthropic: system must be string or array, got %s", trimmed)
}

// decodeMessage maps one Anthropic message to one or more unified messages.
//
//  1. String content → stays as one message with text content.
//  2. Array content:
//     - text blocks → preserved as multipart Content on a user/assistant msg.
//     - tool_use blocks (assistant) → lift into Message.ToolCalls.
//     - tool_result blocks (user)   → split into a separate role=tool message.
//
// A user message mixing text + tool_result becomes two unified messages
// (tool first, then text) so the OpenAI-protocol ordering invariant
// (tool result immediately follows the assistant tool_call) holds.
func decodeMessage(m wireMessage) ([]adapter.Message, error) {
	role := mapRole(m.Role)

	// String content is the common case.
	trimmed := strings.TrimSpace(string(m.Content))
	if trimmed == "" {
		return []adapter.Message{{Role: role}}, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			return nil, fmt.Errorf("anthropic: parse %s content string: %w", m.Role, err)
		}
		return []adapter.Message{{Role: role, Content: adapter.NewContentText(s)}}, nil
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("anthropic: %s content must be string or array, got %s", m.Role, trimmed)
	}

	var blocks []wireContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, fmt.Errorf("anthropic: parse %s content array: %w", m.Role, err)
	}

	// Split blocks by type. tool_result becomes its own role=tool message;
	// text stays; tool_use lifts into ToolCalls on an assistant message.
	var (
		textParts  []json.RawMessage // raw text-block JSON to preserve verbatim
		toolCalls  []adapter.ToolCall
		toolResult *adapter.Message
	)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			// Preserve the block as-is by re-marshalling; openai adapter
			// forwards multipart content verbatim. We keep only {type,text}.
			part, _ := json.Marshal(struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{Type: "text", Text: b.Text})
			textParts = append(textParts, part)
		case "tool_use":
			// input is a JSON object → openai function.arguments is the JSON
			// string of those arguments. We forward the marshalled bytes.
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, adapter.ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: adapter.FunctionCall{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			// tool_result content may be a string or an array of content
			// blocks. Flatten to text for the unified tool message.
			result := flattenToolResultContent(b.Content)
			toolResult = &adapter.Message{
				Role:       adapter.RoleTool,
				ToolCallID: b.ToolUseID,
				Content:    adapter.NewContentText(result),
			}
		case "image", "image_url":
			return nil, fmt.Errorf("anthropic: unsupported content type: %s (ADR-0045 gap)", b.Type)
		default:
			return nil, fmt.Errorf("anthropic: unsupported content type: %s", b.Type)
		}
	}

	out := make([]adapter.Message, 0, 2)
	// tool_result must immediately follow the assistant tool_use it answers;
	// emit it first so it lands right after the preceding assistant message.
	if toolResult != nil {
		out = append(out, *toolResult)
	}
	// Remaining content goes onto a single message of the original role. Skip
	// the trailing message entirely if it would be empty (a user message that
	// carried only a tool_result has nothing else to say).
	if len(textParts) > 0 || len(toolCalls) > 0 {
		msg := adapter.Message{Role: role}
		if len(textParts) > 0 {
			// Build a multipart Content = "[" + join(parts, ",") + "]".
			arr := append([]byte{'['}, append(joinRaw(textParts, ","), ']')...)
			// Store raw on Content (bypass SetText to preserve multipart form).
			_ = msg.Content.UnmarshalJSON(arr)
		}
		if role == adapter.RoleAssistant && len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		out = append(out, msg)
	}
	return out, nil
}

// flattenToolResultContent accepts a tool_result's content field (string or
// content-block array) and returns its concatenated plain text.
func flattenToolResultContent(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	if trimmed[0] == '[' {
		var blocks []wireContentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return ""
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// mapRole normalizes an Anthropic role string to a unified Role. Anthropic
// uses the same user/assistant vocabulary; anything else passes through.
func mapRole(r string) adapter.Role {
	switch r {
	case "user":
		return adapter.RoleUser
	case "assistant":
		return adapter.RoleAssistant
	case "system":
		return adapter.RoleSystem
	default:
		return adapter.Role(r)
	}
}

// decodeToolChoice maps an Anthropic tool_choice object to the unified OpenAI
// form. Anthropic shapes: {"type":"auto"} | {"type":"any"} | {"type":"tool",
// "name":"..."} | {"type":"none"}.
func decodeToolChoice(raw json.RawMessage) any {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil
	}
	switch tc.Type {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	case "none":
		return "none"
	}
	return nil
}

// ---- EncodeResponse (unified → Anthropic) -------------------------------

func (c *codec) EncodeResponse(resp *adapter.UnifiedResponse) ([]byte, error) {
	// Anthropic Message responses only have ONE content array. A unified
	// response with multiple choices can't be represented; we take choice[0]
	// (the OpenAI n=1 case, which is all Claude Code uses).
	var blocks []map[string]any
	var stopReason string
	if len(resp.Choices) > 0 {
		ch := resp.Choices[0]
		stopReason = mapFinishReasonToStop(ch.FinishReason)
		text := ch.Message.Content.Text()
		if text != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": text,
			})
		}
		for _, tc := range ch.Message.ToolCalls {
			// input is a JSON object; parse the arguments string back into a
			// generic value so it serializes as a nested object (not a string).
			var input any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				input = map[string]any{}
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": input,
			})
		}
	}
	if blocks == nil {
		blocks = []map[string]any{}
	}
	out := map[string]any{
		"id":            resp.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         resp.Model,
		"content":       blocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
	}
	if resp.Usage != nil {
		out["usage"] = map[string]any{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode response: %w", err)
	}
	return b, nil
}

// mapFinishReasonToStop converts OpenAI finish_reason → Anthropic stop_reason.
func mapFinishReasonToStop(s string) string {
	switch s {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "":
		return ""
	default:
		return s
	}
}

// ---- EncodeError --------------------------------------------------------

func (c *codec) EncodeError(_ int, errType, message string) []byte {
	anthType := mapErrTypeToAnthropic(errType)
	body, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthType,
			"message": message,
		},
	})
	return body
}

// mapErrTypeToAnthropic maps OpenAI-style error codes to Anthropic's error
// type vocabulary. Unknown codes fall back to "api_error" (Anthropic's generic
// 5xx type); this matches OpenAI's "api_error" semantics.
func mapErrTypeToAnthropic(t string) string {
	switch t {
	case "authentication_error", "invalid_api_key":
		return "authentication_error"
	case "rate_limit_error":
		return "rate_limit_error"
	case "insufficient_quota", "invalid_request_error", "invalid_request_body", "model_not_permitted":
		return "invalid_request_error"
	case "overloaded_error":
		return "overloaded_error"
	case "upstream_error", "timeout_error", "api_error":
		return "api_error"
	}
	return "api_error"
}

// ---- helpers ------------------------------------------------------------

// joinRaw concatenates raw JSON fragments with the given separator. Used to
// assemble a multipart Content array from individual marshalled blocks.
func joinRaw(parts []json.RawMessage, sep string) []byte {
	if len(parts) == 0 {
		return nil
	}
	out := []byte(parts[0])
	for _, p := range parts[1:] {
		out = append(out, sep...)
		out = append(out, p...)
	}
	return out
}
