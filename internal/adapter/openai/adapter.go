// Package openai implements the Adapter for OpenAI and any OpenAI-compatible
// upstream (Tencent Hunyuan, Zhipu, and generic compatible APIs all reuse this
// adapter; only the BaseURL/credentials differ — see ADR-0001). It is a pure
// translator: it builds a transport-neutral UpstreamRequest and parses response
// bytes/streams, performing no HTTP transport itself (that is the proxy's job).
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"voxeltoad/internal/adapter"
	"voxeltoad/pkg/sse"
)

// adapterName is the registry key. Compatible brands reuse it (ADR-0001).
const adapterName = "openai"

func init() {
	adapter.Register(adapterName, func(cfg any) (adapter.Adapter, error) {
		opts, ok := cfg.(adapter.Options)
		if !ok {
			return nil, fmt.Errorf("openai: expected adapter.Options config, got %T", cfg)
		}
		return New(opts)
	})
}

// Options configures an OpenAI-compatible adapter instance. It is the shared
// adapter.Options (alias) so the registry can construct this adapter generically
// (the proxy/admin layer resolves a config.Provider into it, including secret
// resolution of the API key).
type Options = adapter.Options

// Adapter is the OpenAI-compatible adapter.
type Adapter struct {
	baseURL string
	apiKey  string
}

// New builds an OpenAI-compatible adapter from Options.
func New(opts Options) (*Adapter, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("openai: BaseURL is required")
	}
	return &Adapter{baseURL: opts.BaseURL, apiKey: opts.APIKey}, nil
}

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return adapterName }

// wireRequest is the OpenAI chat-completions request body.
type wireRequest struct {
	Model         string             `json:"model"`
	Messages      []adapter.Message  `json:"messages"`
	Stream        bool               `json:"stream,omitempty"`
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	MaxTokens     *int               `json:"max_tokens,omitempty"`
	Tools         []adapter.Tool     `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// BuildRequest translates a unified request into a transport-neutral
// UpstreamRequest. For streaming, it sets stream_options.include_usage so OpenAI
// emits token usage on the final chunk (the Chunk usage contract).
func (a *Adapter) BuildRequest(_ context.Context, req *adapter.UnifiedRequest) (*adapter.UpstreamRequest, error) {
	wr := wireRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
	}
	if req.Stream {
		wr.StreamOptions = &wireStreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(wr)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	// Merge passthrough Extra fields from the unified request into the
	// upstream body. Known wireRequest fields take priority — Extra entries
	// never overwrite model, messages, stream_options, etc.
	if len(req.Extra) > 0 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("openai: unmarshal body for extra merge: %w", err)
		}
		for k, v := range req.Extra {
			if _, exists := m[k]; !exists && v != nil {
				m[k] = v
			}
		}
		var mergeErr error
		body, mergeErr = json.Marshal(m)
		if mergeErr != nil {
			return nil, fmt.Errorf("openai: marshal body after extra merge: %w", mergeErr)
		}
	}

	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		h.Set("Authorization", "Bearer "+a.apiKey)
	}

	return &adapter.UpstreamRequest{
		Method: http.MethodPost,
		URL:    a.baseURL + "/chat/completions",
		Header: h,
		Body:   body,
	}, nil
}

// wireUsageDetails captures OpenAI's nested usage.prompt_tokens_details, which
// the flat adapter.Usage cannot decode directly. Only the cache fields we need.
type wireUsageDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// wireUsageEnvelope is the minimal shape overlaying the response body to extract
// nested prompt cache details without disturbing the direct UnifiedResponse
// unmarshal (which preserves upstream fields verbatim via resp.Raw).
type wireUsageEnvelope struct {
	Usage *struct {
		PromptTokensDetails *wireUsageDetails `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// ParseResponse parses a non-streaming chat-completion response body. The OpenAI
// wire format already matches UnifiedResponse, so this is a direct unmarshal;
// the nested prompt_tokens_details.cached_tokens is decoded separately and
// folded back into resp.Usage.CachedPromptTokens (OpenAI semantics: prompt_tokens
// already includes the cached portion).
func (a *Adapter) ParseResponse(body []byte) (*adapter.UnifiedResponse, error) {
	var resp adapter.UnifiedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("openai: parse response: %w", err)
	}
	resp.Raw = body
	resp.RawProtocol = "openai"
	// Decode nested cache details separately (adapter.Usage is flat by design).
	if resp.Usage != nil {
		var env wireUsageEnvelope
		if err := json.Unmarshal(body, &env); err == nil &&
			env.Usage != nil && env.Usage.PromptTokensDetails != nil {
			resp.Usage.CachedPromptTokens = env.Usage.PromptTokensDetails.CachedTokens
		}
	}
	return &resp, nil
}

// ExtractUsage returns the response's usage, or an error if absent.
func (a *Adapter) ExtractUsage(resp *adapter.UnifiedResponse) (*adapter.Usage, error) {
	if resp == nil || resp.Usage == nil {
		return nil, errors.New("openai: usage unavailable")
	}
	return resp.Usage, nil
}

// ParseStream decodes an SSE chat-completion stream into unified Chunks.
func (a *Adapter) ParseStream(body io.Reader) (adapter.StreamReader, error) {
	return &streamReader{dec: sse.NewDecoder(body)}, nil
}

// wireStreamUsage mirrors adapter.Usage plus the nested prompt_tokens_details
// field that OpenAI's streaming trailing chunk carries. Decoded separately so
// adapter.Usage stays flat.
type wireStreamUsage struct {
	PromptTokens        int               `json:"prompt_tokens"`
	CompletionTokens    int               `json:"completion_tokens"`
	TotalTokens         int               `json:"total_tokens"`
	PromptTokensDetails *wireUsageDetails `json:"prompt_tokens_details"`
}

// wireStreamChunk is one OpenAI streaming chunk payload.
type wireStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string                    `json:"role"`
			Content   string                    `json:"content"`
			ToolCalls []wireStreamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireStreamUsage `json:"usage"`
}

// wireStreamToolCallDelta mirrors the OpenAI delta.tool_calls[*] entry. The
// first chunk for a tool call carries Index/ID/Type/Function.Name; subsequent
// chunks with the same Index carry only another Function.Arguments fragment.
type wireStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type streamReader struct {
	dec *sse.Decoder
}

// Recv returns the next unified Chunk, or io.EOF at end of stream. The "[DONE]"
// sentinel terminates the stream (returned as io.EOF, not a chunk).
func (s *streamReader) Recv() (adapter.Chunk, error) {
	ev, err := s.dec.Next()
	if err != nil {
		return adapter.Chunk{}, err // includes io.EOF
	}
	if ev.Data == sse.Done {
		return adapter.Chunk{}, io.EOF
	}
	var wc wireStreamChunk
	if err := json.Unmarshal([]byte(ev.Data), &wc); err != nil {
		return adapter.Chunk{}, fmt.Errorf("openai: parse stream chunk: %w", err)
	}

	c := adapter.Chunk{ID: wc.ID, Model: wc.Model, Raw: json.RawMessage(ev.Data), RawProtocol: "openai"}
	if wc.Usage != nil {
		u := &adapter.Usage{
			PromptTokens:     wc.Usage.PromptTokens,
			CompletionTokens: wc.Usage.CompletionTokens,
			TotalTokens:      wc.Usage.TotalTokens,
		}
		if wc.Usage.PromptTokensDetails != nil {
			u.CachedPromptTokens = wc.Usage.PromptTokensDetails.CachedTokens
		}
		c.Usage = u
	}
	if len(wc.Choices) > 0 {
		ch := wc.Choices[0]
		c.DeltaRole = adapter.Role(ch.Delta.Role)
		c.DeltaContent = ch.Delta.Content
		c.FinishReason = ch.FinishReason
		if len(ch.Delta.ToolCalls) > 0 {
			c.DeltaToolCalls = make([]adapter.ToolCallDelta, len(ch.Delta.ToolCalls))
			for i, tc := range ch.Delta.ToolCalls {
				c.DeltaToolCalls[i] = adapter.ToolCallDelta{
					Index: tc.Index,
					ID:    tc.ID,
					Type:  tc.Type,
					Function: adapter.FunctionCallDelta{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
	}
	return c, nil
}

func (s *streamReader) Close() error { return nil }
