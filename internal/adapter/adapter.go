// Package adapter defines the provider adapter abstraction. Each upstream
// provider (OpenAI, Claude, Tencent Hunyuan, Zhipu, or any OpenAI-compatible
// API) implements Adapter to translate between the gateway's unified,
// OpenAI-compatible request/response model and the provider's native protocol.
//
// See design/architecture.md ("新增一个供应商适配器") for the steps to add a
// new provider.
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Content is the content of a chat message. It preserves the original JSON wire
// form byte-for-byte (string, null, or multipart array), eliminating the data
// loss that occurs when content is modelled as a plain Go string.
//
// Accessors (Text, SetText, IsNull, IsMultipart) let plugins and normalization
// read/write text safely without forcing a specific JSON shape. The raw-backed
// design means assistant messages with tool_calls (content: null) survive a
// round-trip unchanged, and multipart arrays ([{type:text,...},{type:image_url,...}])
// are forwarded without loss.
type Content struct {
	raw json.RawMessage
}

// NewContentText returns a Content set to the given plain-text string.
func NewContentText(s string) Content {
	var c Content
	c.SetText(s)
	return c
}

// UnmarshalJSON captures the raw JSON bytes as-is, preserving string,
// null, and array forms without interpretation.
func (c *Content) UnmarshalJSON(data []byte) error {
	c.raw = make(json.RawMessage, len(data))
	copy(c.raw, data)
	return nil
}

// MarshalJSON returns the original raw JSON. If no raw has been set
// (zero value), it emits null — matching the OpenAI spec for assistant
// messages that carry tool_calls but no text content.
func (c Content) MarshalJSON() ([]byte, error) {
	if len(c.raw) == 0 {
		return []byte("null"), nil
	}
	return c.raw, nil
}

// Text returns the plain-text representation of the content. For a
// JSON-string value it returns the string (unquoted). For a multipart
// array it concatenates all {"type":"text",...} parts. For null or
// empty it returns "".
func (c Content) Text() string {
	if len(c.raw) == 0 {
		return ""
	}
	// JSON string (with surrounding quotes).
	if bytes.HasPrefix(c.raw, []byte{'"'}) {
		var s string
		if err := json.Unmarshal(c.raw, &s); err == nil {
			return s
		}
		return ""
	}
	// JSON array — collect text parts.
	if bytes.HasPrefix(c.raw, []byte{'['}) {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(c.raw, &parts); err == nil {
			var b strings.Builder
			for _, p := range parts {
				if p.Type == "text" {
					b.WriteString(p.Text)
				}
			}
			return b.String()
		}
	}
	// null or unrecognised.
	return ""
}

// SetText replaces the content with a JSON string holding s. If the
// previous content was a multipart array this destroys non-text
// parts; callers that need to preserve them should manipulate raw
// directly.
func (c *Content) SetText(s string) {
	b, _ := json.Marshal(s)
	c.raw = json.RawMessage(b)
}

// IsNull reports whether the content is the JSON literal null.
func (c Content) IsNull() bool {
	return bytes.Equal(c.raw, []byte("null"))
}

// IsMultipart reports whether the content is a JSON array (multipart).
func (c Content) IsMultipart() bool {
	return bytes.HasPrefix(c.raw, []byte{'['})
}

// Role identifies the author of a message in a chat completion.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single chat message in the unified (OpenAI-compatible) format.
type Message struct {
	Role       Role       `json:"role"`
	Content    Content    `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a tool call requested by the model on an assistant message.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall describes a function invocation within a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// FunctionDef is the schema of a tool the model may call. Parameters is the
// JSON schema for the function's arguments, carried verbatim (json.RawMessage)
// so the gateway never has to understand a provider's schema dialect.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Tool is a tool definition offered to the model on the request side. Only
// "function" tools are modeled (the OpenAI-compatible standard).
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// UnifiedRequest is the gateway's internal, OpenAI-compatible request model.
// Adapters translate it into provider-native requests.
type UnifiedRequest struct {
	// Model is the upstream model name, already resolved by the routing layer
	// from the client's alias (see ADR-0002). Adapters receive the upstream
	// name and send it as-is; they never see or resolve aliases.
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	// Tools are the function definitions offered to the model (OpenAI-compatible
	// "tools" field). Adapters that support tool calling forward them verbatim.
	Tools []Tool `json:"tools,omitempty"`
	// ToolChoice controls tool selection. It is typed as any because the
	// OpenAI-compatible wire form has three shapes: "auto"/"none" (string) or
	// {"type":"function","function":{"name":...}} (object). It is forwarded as-is.
	ToolChoice any `json:"tool_choice,omitempty"`

	// Extra carries provider- or passthrough-specific fields that the gateway
	// does not model explicitly. Fields that do not have a matching struct member
	// in UnifiedRequest are automatically captured here during UnmarshalJSON
	// and merged back during MarshalJSON. Known fields (model, messages, etc.)
	// take priority and are never overwritten by Extra entries.
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON captures known fields into UnifiedRequest struct members and
// preserves all unknown fields verbatim in Extra as json.RawMessage. This
// ensures client-supplied parameters (response_format, n, stop, seed, etc.)
// are never silently discarded.
func (r *UnifiedRequest) UnmarshalJSON(data []byte) error {
	// Decode into a map first to capture all fields.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Populate known fields from the map.
	if v, ok := raw["model"]; ok {
		if err := json.Unmarshal(v, &r.Model); err != nil {
			return err
		}
		delete(raw, "model")
	}
	if v, ok := raw["messages"]; ok {
		if err := json.Unmarshal(v, &r.Messages); err != nil {
			return err
		}
		delete(raw, "messages")
	}
	if v, ok := raw["stream"]; ok {
		if err := json.Unmarshal(v, &r.Stream); err != nil {
			return err
		}
		delete(raw, "stream")
	}
	if v, ok := raw["temperature"]; ok {
		if err := json.Unmarshal(v, &r.Temperature); err != nil {
			return err
		}
		delete(raw, "temperature")
	}
	if v, ok := raw["max_tokens"]; ok {
		if err := json.Unmarshal(v, &r.MaxTokens); err != nil {
			return err
		}
		delete(raw, "max_tokens")
	}
	if v, ok := raw["tools"]; ok {
		if err := json.Unmarshal(v, &r.Tools); err != nil {
			return err
		}
		delete(raw, "tools")
	}
	if v, ok := raw["tool_choice"]; ok {
		if err := json.Unmarshal(v, &r.ToolChoice); err != nil {
			return err
		}
		delete(raw, "tool_choice")
	}

	r.Extra = raw
	return nil
}

// MarshalJSON produces a JSON object with all known fields plus any Extra
// entries. Known fields take priority: an Extra entry whose key collides with
// a known field is silently dropped.
func (r UnifiedRequest) MarshalJSON() ([]byte, error) {
	// Marshal known fields into a map.
	b, err := json.Marshal(struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		Stream      bool      `json:"stream"`
		Temperature *float64  `json:"temperature,omitempty"`
		MaxTokens   *int      `json:"max_tokens,omitempty"`
		Tools       []Tool    `json:"tools,omitempty"`
		ToolChoice  any       `json:"tool_choice,omitempty"`
	}{
		Model: r.Model, Messages: r.Messages, Stream: r.Stream,
		Temperature: r.Temperature, MaxTokens: r.MaxTokens,
		Tools: r.Tools, ToolChoice: r.ToolChoice,
	})
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}

	// Merge Extra: Extra entries never overwrite known keys.
	for k, v := range r.Extra {
		if _, known := out[k]; !known && v != nil {
			out[k] = v
		}
	}
	return json.Marshal(out)
}

// UnifiedResponse is the gateway's internal, OpenAI-compatible response model
// for non-streaming completions.
type UnifiedResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`

	// UpstreamRequestID is the provider-assigned request correlation id
	// extracted from the upstream response body, when the provider puts it
	// there (e.g. Anthropic's request_id). The Forwarder overrides this with
	// the value from the upstream response header when available, since the
	// header is the authoritative request-level id (the body field may be a
	// different, completion-scoped id on some providers). Distinct from ID,
	// which is the chat completion id (chatcmpl-… / msg_…). Empty when the
	// provider returned no id in either location.
	UpstreamRequestID string `json:"-"`

	// Raw is the original upstream response body, preserved byte-for-byte.
	// For OpenAI→OpenAI paths, the proxy may write Raw directly to the client
	// to avoid data loss from a re-encode round-trip (system_fingerprint,
	// logprobs, extra choice fields, etc.). Set by ParseResponse; nil when
	// parsing is not source-preserving.
	Raw json.RawMessage `json:"-"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// Chunk is one streamed delta in the unified format. A stream is an ordered
// sequence of Chunks.
//
// Usage contract: intermediate chunks have Usage == nil. Token usage arrives
// only in the trailing chunk(s) just before the stream ends — for OpenAI this
// requires stream_options.include_usage; for Claude it comes on the
// message_delta event. Billing consumes this trailing Usage (see
// design/observability.md and design/e2e.md Pitfalls). Adapters MUST NOT
// fabricate per-chunk usage.
// FunctionCallDelta is the incremental function fragment of a streamed tool
// call. On the first chunk for a tool call it carries the name (and possibly a
// leading arguments fragment); subsequent chunks with the same Index carry only
// the next arguments fragment. Arguments may therefore be partial JSON.
type FunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolCallDelta is one streamed tool-call delta (the OpenAI-compatible
// delta.tool_calls entry). Index groups fragments of the same tool call across
// chunks so the client can reassemble the full call.
type ToolCallDelta struct {
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function FunctionCallDelta `json:"function"`
}

type Chunk struct {
	ID             string          `json:"id"`
	Model          string          `json:"model"`
	DeltaRole      Role            `json:"-"`
	DeltaContent   string          `json:"-"`
	DeltaToolCalls []ToolCallDelta `json:"-"`
	FinishReason   string          `json:"finish_reason,omitempty"`
	Usage          *Usage          `json:"usage,omitempty"`

	// Raw is the original upstream SSE data line, preserved byte-for-byte.
	// For OpenAI→OpenAI paths, the proxy may write Raw directly to the
	// client to avoid data loss from a re-encode round-trip. Set by
	// streamReader.Recv; nil for adapters that do not preserve raw chunks
	// (e.g., Claude, which must re-encode into OpenAI chunk format).
	Raw json.RawMessage `json:"-"`
}

// Usage holds token accounting. Billing MUST prefer the usage returned by the
// upstream response over any local estimate (see design/e2e.md Pitfalls).
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedPromptTokens is the portion of PromptTokens that hit the upstream
	// provider's prompt cache (OpenAI prompt_tokens_details.cached_tokens;
	// Claude cache_read_input_tokens). Billing applies CacheHitMultiplier to
	// this portion; the rest of PromptTokens is billed at full rate. 0 = no
	// cache hit. Claude's cache_creation_input_tokens is NOT included here — it
	// is folded into PromptTokens at full price (creation is a write, not a
	// read hit). OpenAI semantics: PromptTokens already contains this value
	// (non-cached = PromptTokens - CachedPromptTokens). Claude semantics:
	// PromptTokens = input + cache_creation + cache_read (sum is the full
	// billable prompt).
	CachedPromptTokens int `json:"cached_tokens,omitempty"`
}

// StreamReader yields unified Chunks from an upstream streaming response.
// Recv returns io.EOF when the stream is exhausted. Implementations must be
// safe to Close more than once.
type StreamReader interface {
	Recv() (Chunk, error)
	Close() error
}

// UpstreamRequest is a transport-neutral description of the HTTP request an
// adapter wants the proxy to send upstream. Adapters produce this (pure
// translation, fully testable by asserting fields); the proxy turns it into an
// *http.Request and owns transport, timeouts, and retries. This keeps the
// Adapter contract symmetric: bytes/values in and out, no *http.Request on
// either side.
type UpstreamRequest struct {
	// Method is the HTTP method (usually POST).
	Method string
	// URL is the fully-resolved upstream URL.
	URL string
	// Header carries request headers (auth, content-type, etc.).
	Header http.Header
	// Body is the serialized request body (already provider-native JSON).
	Body []byte
}

// Options is the shared configuration every adapter factory accepts. The
// proxy/admin layer resolves a config.Provider into Options (resolving the API
// key secret) and constructs any adapter generically via New(name, Options),
// without importing concrete adapter packages. Adapters needing extra knobs may
// embed or alias this type.
type Options struct {
	// BaseURL is the upstream API base (e.g. "https://api.openai.com/v1").
	BaseURL string
	// APIKey is the already-resolved plaintext credential (see config.ResolveSecret).
	APIKey string
}

// Adapter translates between the unified request/response model and a specific
// provider's native protocol. Adapters are pure translators: they do not
// perform HTTP transport, timeouts, or retries — that is the proxy's job (see
// design/architecture.md). Every method is values-in, values-out (no
// *http.Request / *http.Response), which keeps adapters testable with
// byte-level testdata samples (see design/unit-test.md).
type Adapter interface {
	// Name returns the provider identifier (e.g. "openai", "claude").
	Name() string

	// BuildRequest translates a unified request into a transport-neutral
	// UpstreamRequest. The proxy converts it to an *http.Request and sends it
	// (transport, timeouts, retries).
	BuildRequest(ctx context.Context, req *UnifiedRequest) (*UpstreamRequest, error)

	// ParseResponse parses a non-streaming upstream response body into the
	// unified format. The proxy reads the body and hands the bytes here, so
	// parsing is decoupled from transport and trivially testable.
	ParseResponse(body []byte) (*UnifiedResponse, error)

	// ParseStream wraps a streaming (SSE) upstream response body as a
	// StreamReader that yields unified Chunks. The proxy passes the response
	// body reader; the adapter owns SSE decoding (typically via pkg/sse).
	ParseStream(body io.Reader) (StreamReader, error)

	// ExtractUsage pulls token usage out of a parsed unified response for
	// billing. Returns an error if usage is unavailable.
	ExtractUsage(resp *UnifiedResponse) (*Usage, error)
}
