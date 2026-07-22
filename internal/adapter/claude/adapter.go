// Package claude implements the Adapter for Anthropic's Messages API. It is a
// pure translator (semantic rewriting like system extraction / alternation is
// done by the normalization layer, ADR-0009): it maps the unified model to
// Anthropic's wire format, detects stream end on the message_stop event (no
// [DONE]; ADR-0010), and assembles usage from message_start (input) +
// message_delta (output) so the Chunk usage contract holds unchanged.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"voxeltoad/internal/adapter"
	"voxeltoad/pkg/sse"
)

const (
	adapterName      = "claude"
	anthropicVersion = "2023-06-01"
)

func init() {
	adapter.Register(adapterName, func(cfg any) (adapter.Adapter, error) {
		opts, ok := cfg.(adapter.Options)
		if !ok {
			return nil, fmt.Errorf("claude: expected adapter.Options config, got %T", cfg)
		}
		return New(opts)
	})
}

// Options configures a Claude adapter instance. It is the shared adapter.Options
// (alias) so the registry can construct this adapter generically.
type Options = adapter.Options

// Adapter is the Anthropic Messages adapter.
type Adapter struct {
	baseURL string
	apiKey  string
}

// New builds a Claude adapter from Options.
func New(opts Options) (*Adapter, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("claude: BaseURL is required")
	}
	return &Adapter{baseURL: opts.BaseURL, apiKey: opts.APIKey}, nil
}

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return adapterName }

type wireRequest struct {
	Model     string        `json:"model"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream,omitempty"`
	Temp      *float64      `json:"temperature,omitempty"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// BuildRequest maps the unified request to Anthropic's Messages API. The system
// message (the normalization layer guarantees at most one leading system) is
// lifted to the top-level system field; remaining messages map directly.
func (a *Adapter) BuildRequest(_ context.Context, req *adapter.UnifiedRequest) (*adapter.UpstreamRequest, error) {
	wr := wireRequest{Model: req.Model, Stream: req.Stream, Temp: req.Temperature}
	for _, m := range req.Messages {
		if m.Role == adapter.RoleSystem {
			if wr.System == "" {
				wr.System = m.Content.Text()
			} else {
				wr.System += "\n\n" + m.Content.Text()
			}
			continue
		}
		wr.Messages = append(wr.Messages, wireMessage{Role: string(m.Role), Content: m.Content.Text()})
	}
	if req.MaxTokens != nil {
		wr.MaxTokens = *req.MaxTokens
	}

	body, err := json.Marshal(wr)
	if err != nil {
		return nil, fmt.Errorf("claude: marshal request: %w", err)
	}

	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("anthropic-version", anthropicVersion)
	if a.apiKey != "" {
		h.Set("x-api-key", a.apiKey)
	}

	return &adapter.UpstreamRequest{
		Method: http.MethodPost,
		URL:    a.baseURL + "/v1/messages",
		Header: h,
		Body:   body,
	}, nil
}

type wireResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	// RequestID is the provider-assigned request id. Anthropic returns it in
	// the response header (request-id) AND in the body; the Forwarder
	// prefers the header. Body is a fallback used when the header is absent.
	RequestID string `json:"request_id"`
}

// ParseResponse maps a non-streaming Messages response to the unified format.
func (a *Adapter) ParseResponse(body []byte) (*adapter.UnifiedResponse, error) {
	var wr wireResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("claude: parse response: %w", err)
	}
	var text string
	for _, b := range wr.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return &adapter.UnifiedResponse{
		ID:    wr.ID,
		Model: wr.Model,
		Choices: []adapter.Choice{{
			Index:        0,
			Message:      adapter.Message{Role: adapter.RoleAssistant, Content: adapter.NewContentText(text)},
			FinishReason: mapStopReason(wr.StopReason),
		}},
		Usage:             usageOf(wr.Usage.InputTokens, wr.Usage.CacheCreationInputTokens, wr.Usage.CacheReadInputTokens, wr.Usage.OutputTokens),
		UpstreamRequestID: wr.RequestID, // body fallback; Forwarder overrides with the response header when present
		Raw:               body, // preserved for passthrough (ADR-0047): when the client's ingress protocol is anthropic and this claude adapter was hit, the anthropic codec emits Raw verbatim.
		RawProtocol:       "anthropic",
	}, nil
}

// ExtractUsage returns the response's usage, or an error if absent.
func (a *Adapter) ExtractUsage(resp *adapter.UnifiedResponse) (*adapter.Usage, error) {
	if resp == nil || resp.Usage == nil {
		return nil, errors.New("claude: usage unavailable")
	}
	return resp.Usage, nil
}

// mapStopReason maps Anthropic stop_reason to OpenAI-style finish_reason.
func mapStopReason(s string) string {
	switch s {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "":
		return ""
	default:
		return s
	}
}

// usageOf assembles unified usage from Claude's token fields. Claude reports
// cache reads/creations separately from the base input_tokens; PromptTokens is
// the sum (the full billable prompt), and CachedPromptTokens captures the cache
// read hit portion (billed at CacheHitMultiplier). cache_creation is folded
// into PromptTokens at full price (it is a cache write, not a read hit).
func usageOf(input, cacheCreation, cacheRead, output int) *adapter.Usage {
	prompt := input + cacheCreation + cacheRead
	return &adapter.Usage{
		PromptTokens:       prompt,
		CompletionTokens:   output,
		TotalTokens:        prompt + output,
		CachedPromptTokens: cacheRead,
	}
}

// ParseStream decodes the Anthropic SSE stream into unified Chunks.
func (a *Adapter) ParseStream(body io.Reader) (adapter.StreamReader, error) {
	return &streamReader{dec: sse.NewDecoder(body)}, nil
}

type streamReader struct {
	dec               *sse.Decoder
	inputToks         int // buffered from message_start (ADR-0010)
	cacheCreationToks int // buffered from message_start (prompt cache write)
	cacheReadToks     int // buffered from message_start (prompt cache read hit)
	haveInput         bool
}

// Recv returns the next unified Chunk, or io.EOF at end of stream. End is
// detected on the message_stop event (Anthropic has no [DONE]). Usage is
// assembled: input_tokens buffered from message_start is combined with
// output_tokens from message_delta and emitted on that trailing chunk.
func (s *streamReader) Recv() (adapter.Chunk, error) {
	for {
		ev, err := s.dec.Next()
		if err != nil {
			return adapter.Chunk{}, err // includes io.EOF
		}

		switch ev.Event {
		case "message_start":
			var p struct {
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage struct {
						InputTokens              int `json:"input_tokens"`
						CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
						CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
				return adapter.Chunk{}, fmt.Errorf("claude: parse message_start: %w", err)
			}
			s.inputToks = p.Message.Usage.InputTokens
			s.cacheCreationToks = p.Message.Usage.CacheCreationInputTokens
			s.cacheReadToks = p.Message.Usage.CacheReadInputTokens
			s.haveInput = true
			// Return a chunk so: (a) the translating path's streamEncoder fires
			// its messageStarted state on the first chunk it sees; (b) the
			// passthrough path can replay the original message_start frame from
			// Raw (ADR-0047). DeltaRole carries no content but signals "this is
			// the opening chunk" — the ingress anthropic encoder starts the
			// message on the first non-empty chunk it receives.
			return adapter.Chunk{
				ID:          p.Message.ID,
				Model:       p.Message.Model,
				DeltaRole:   adapter.RoleAssistant,
				Raw:         reassembleSSEFrame(ev.Event, ev.Data),
				RawProtocol: "anthropic",
			}, nil

		case "content_block_delta":
			var p struct {
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
				return adapter.Chunk{}, fmt.Errorf("claude: parse content_block_delta: %w", err)
			}
			return adapter.Chunk{DeltaContent: p.Delta.Text, Raw: reassembleSSEFrame(ev.Event, ev.Data), RawProtocol: "anthropic"}, nil

		case "message_delta":
			var p struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
				return adapter.Chunk{}, fmt.Errorf("claude: parse message_delta: %w", err)
			}
			// Trailing chunk: finish reason + assembled usage. Raw carries the
			// original message_delta frame for passthrough (ADR-0047).
			return adapter.Chunk{
				FinishReason: mapStopReason(p.Delta.StopReason),
				Usage:        usageOf(s.inputToks, s.cacheCreationToks, s.cacheReadToks, p.Usage.OutputTokens),
				Raw:          reassembleSSEFrame(ev.Event, ev.Data),
				RawProtocol:  "anthropic",
			}, nil

		case "message_stop":
			// message_stop has no payload beyond the event type. The ingress
			// anthropic codec's StreamTerminator emits the client-facing
			// message_stop; we don't need to relay it as a Chunk (the codec's
			// Close/terminator handles stream end in both translating and
			// passthrough modes).
			return adapter.Chunk{}, io.EOF

		case "content_block_start", "content_block_stop":
			// Block-boundary events carry no unified semantics (the translating
			// ingress codec synthesizes its own start/stop from DeltaContent /
			// DeltaToolCalls), but passthrough mode (ADR-0047) MUST relay them
			// verbatim or the client's Anthropic stream is missing the block
			// boundaries — tool_use blocks become unparseable. Return a Chunk
			// whose only payload is the reassembled Raw frame; the translating
			// encoder ignores Raw-free semantic fields (empty DeltaContent etc.)
			// while the passthrough encoder relays Raw as-is.
			return adapter.Chunk{
				Raw:         reassembleSSEFrame(ev.Event, ev.Data),
				RawProtocol: "anthropic",
			}, nil

		default:
			// ping, unknown event types, etc. — ignore.
		}
	}
}

func (s *streamReader) Close() error { return nil }

// reassembleSSEFrame rebuilds the complete SSE wire frame (event: + data:
// lines + blank terminator) for a decoded event, so the anthropic ingress
// codec can relay it byte-for-byte in passthrough mode (ADR-0047). The
// upstream's raw bytes are not retained by pkg/sse.Decoder, so the frame is
// reassembled from the decoded fields. Multi-line data is split across
// multiple data: lines per the SSE spec (mirrors sse.Encode).
func reassembleSSEFrame(event, data string) []byte {
	var b []byte
	if event != "" {
		b = append(b, "event: "...)
		b = append(b, event...)
		b = append(b, '\n')
	}
	for _, line := range strings.Split(data, "\n") {
		b = append(b, "data: "...)
		b = append(b, line...)
		b = append(b, '\n')
	}
	b = append(b, '\n')
	return b
}
