package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/apperr"
	"voxeltoad/internal/plugin"
	"voxeltoad/pkg/sse"
)

// wireStreamChunk is the OpenAI-compatible chat.completion.chunk shape the proxy
// emits downstream. Re-encoding unified chunks (rather than passing upstream
// bytes through) gives clients a consistent OpenAI-compatible stream regardless
// of which provider served the request.
type wireStreamChunk struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Model   string             `json:"model"`
	Choices []wireStreamChoice `json:"choices"`
	Usage   *adapter.Usage     `json:"usage,omitempty"`
}

// wireStreamChoice / wireStreamDelta are the per-chunk choice and incremental
// delta of the OpenAI chat.completion.chunk shape (delta carries only the new
// token(s) for this chunk).
type wireStreamChoice struct {
	Index        int             `json:"index"`
	Delta        wireStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type wireStreamDelta struct {
	Role      string                    `json:"role,omitempty"`
	Content   string                    `json:"content,omitempty"`
	ToolCalls []wireStreamToolCallDelta `json:"tool_calls,omitempty"`
}

// wireStreamToolCallDelta is the OpenAI-compatible delta.tool_calls[*] entry,
// emitted downstream so clients can reassemble streamed tool calls (Index groups
// fragments of the same call).
type wireStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// streamChatCompletions relays a streaming completion to the client as
// OpenAI-compatible SSE. It flushes after every chunk (so TTFT is preserved and
// nothing is buffered), and always terminates the client stream with a [DONE]
// sentinel — even if the upstream drops mid-stream — so clients never hang.
func streamChatCompletions(w http.ResponseWriter, r *http.Request, disp *Dispatcher, alias string, req *adapter.UnifiedRequest, chain *plugin.Chain, pc *plugin.Context, acc *telemetryAcc) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		acc.errType = apperr.StreamingUnsupported.Code
		writeAppErr(w, apperr.StreamingUnsupported)
		return
	}

	sr, dr, err := disp.ForwardStream(r.Context(), alias, req)
	if err != nil {
		// Nothing written yet, so we can still send a normal error response.
		status, typ := mapForwardError(err)
		acc.errType = typ
		acc.errMsg = truncate([]byte(err.Error()), 256)
		acc.setResult(dr, nil)
		acc.captureError(status, upstreamErrorBody(err))
		logForwardFailure(r, acc.requestID, acc.sessionID, req.Model, dr.Provider, typ, err)
		writeError(w, status, typ, err.Error())
		return
	}
	defer func() { _ = sr.Close() }()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Accumulate the usage seen on the stream so the completion hook can bill
	// what was actually received — even if the stream drops before the trailing
	// usage chunk (partial-stream billing, ADR-0012).
	var lastUsage *adapter.Usage
	firstChunk := true

	// Always close the client stream with [DONE] and run the completion hook,
	// whatever happens below (normal end, drop, or client disconnect).
	defer func() {
		_, _ = w.Write(sse.Encode(sse.Event{Data: sse.Done}))
		flusher.Flush()
		runPost(chain, pc, &adapter.UnifiedResponse{Model: req.Model, Usage: lastUsage}, dr.Provider)
		acc.setResult(dr, lastUsage)
	}()

	for {
		chunk, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			// Upstream dropped or errored mid-stream. Headers (200) are already
			// sent, so we cannot change status; we stop relaying and the
			// deferred [DONE] + completion hook still run on what was received.
			return
		}

		// TTFT: time from request entry to the first relayed chunk (ADR:
		// design/observability.md — measured on the first successful Recv).
		if firstChunk {
			acc.ttft = time.Since(acc.start)
			firstChunk = false
		}

		if chunk.Usage != nil {
			lastUsage = chunk.Usage
		}

		// Prefer the original upstream SSE data line when available, avoiding
		// data loss from a re-encode round-trip (system_fingerprint, logprobs,
		// multi-choice deltas, extra fields). Fall back to re-encoding when
		// Raw is nil (e.g., Claude adapter re-encodes into OpenAI format).
		var data string
		if cData := string(chunk.Raw); cData != "" {
			data = cData
		} else {
			b, mErr := json.Marshal(toWireChunk(chunk))
			if mErr != nil {
				return
			}
			data = string(b)
		}
		// Encode the complete SSE wire frame once (the exact bytes the client
		// receives, including the `data: ` prefix and `\n\n` delimiter).
		wire := sse.Encode(sse.Event{Data: data})
		// Capture the full wire-frame transcript + finish reason for the trace
		// ledger (ADR-0039). Best-effort; a no-op when capture is disabled.
		acc.captureStreamChunk(wire, chunk.FinishReason)
		if _, wErr := w.Write(wire); wErr != nil {
			return // client went away
		}
		flusher.Flush()
	}
}

// toWireChunk converts a unified adapter.Chunk into the OpenAI-compatible wire
// shape. A usage-only chunk (no delta, no tool calls, and no finish reason) is
// emitted with an empty choices array, matching OpenAI's trailing usage chunk
// convention.
func toWireChunk(c adapter.Chunk) wireStreamChunk {
	out := wireStreamChunk{
		ID:      c.ID,
		Object:  "chat.completion.chunk",
		Model:   c.Model,
		Choices: []wireStreamChoice{},
		Usage:   c.Usage,
	}
	// A usage-only chunk (no delta/finish/tool_calls) carries no choice entry,
	// matching OpenAI's trailing usage chunk.
	if c.DeltaRole != "" || c.DeltaContent != "" || c.FinishReason != "" || len(c.DeltaToolCalls) > 0 {
		choice := wireStreamChoice{
			Delta: wireStreamDelta{
				Role:    string(c.DeltaRole),
				Content: c.DeltaContent,
			},
		}
		if len(c.DeltaToolCalls) > 0 {
			choice.Delta.ToolCalls = make([]wireStreamToolCallDelta, len(c.DeltaToolCalls))
			for i, tc := range c.DeltaToolCalls {
				w := &choice.Delta.ToolCalls[i]
				w.Index = tc.Index
				w.ID = tc.ID
				w.Type = tc.Type
				w.Function.Name = tc.Function.Name
				w.Function.Arguments = tc.Function.Arguments
			}
		}
		if c.FinishReason != "" {
			fr := c.FinishReason
			choice.FinishReason = &fr
		}
		out.Choices = []wireStreamChoice{choice}
	}
	return out
}
