package proxy

import (
	"context"
	"encoding/json"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/plugin"
)

// telemetryAcc accumulates a request's semantic fields across the handler's
// branches so a single deferred emit can report them on EVERY exit path
// (success, auth/permission/quota/ratelimit rejection, upstream error). It is
// the one collection point feeding both telemetry (trace+metric, exported) and
// the audit ledger (request_logs, stored) — see design/observability.md.
//
// When a trace-payload recorder is configured (capture enabled, ADR-0039), it
// ALSO accumulates the message + raw layers so the same deferred emit can fan
// them out to the trace_payloads ledger. Payload accumulation is best-effort and
// only populated by the branches that have the bodies; a missing body is simply
// omitted — it never affects the request path.
type telemetryAcc struct {
	start          time.Time
	modelRequested string
	stream         bool

	provider      string
	modelResolved string
	fallback      bool
	retryCount    int
	usage         *adapter.Usage
	ttft          time.Duration
	errType       string
	errMsg        string // truncated underlying cause (e.g. "upstream returned 500: ...")
	blockedBy     string

	requestID         string // gateway-assigned per-request correlation id
	sessionID         string // client-supplied session key (X-Voxeltoad-Session header)
	traceID           string // W3C trace id parsed from traceparent (empty if absent/invalid)
	sessionSource     string // origin label of sessionID (observability only)
	agentType         string // detected agent/client type (claude-code, codex, …; "" if unknown)
	ingressProtocol   string // client wire shape that served this request (openai/anthropic, ADR-0045)
	upstreamRequestID string // provider-assigned id from the successful attempt's response header/body

	// Trace-payload capture (ADR-0039). Populated only by branches that hold the
	// bodies (request read, upstream success, upstream failure, stream). The
	// capture methods read settings() on each call so whether capture happens —
	// and the body cap — are hot-reloadable from the admin UI without a gateway
	// restart. When settings().Trace.CapturePayloadEnabled is false the capture
	// methods short-circuit before any allocation, so disabled capture is
	// zero-cost — no body copy, no marshal, no transcript assembly.
	settings func() *config.GatewaySettings
	tracePL  tracePayloadAcc
}

// tracePayloadAcc holds the message + raw layers accumulated for the trace
// ledger. Fields are populated opportunistically; any may be its zero value.
type tracePayloadAcc struct {
	// requestRaw is the original client request body (the handler reads it once).
	requestRaw json.RawMessage
	// messages is the normalized request message array, marshaled from the
	// UnifiedRequest the gateway routed on.
	messages json.RawMessage
	// responseRaw is the upstream response body: resp.Raw for non-streaming, or
	// the reassembled SSE transcript for streaming. Kept as []byte for efficient
	// streaming append; converted to string when emitted to TracePayload.
	responseRaw json.RawMessage
	// errorRaw is the upstream error body surfaced on failure.
	errorRaw string
	// statusCode is the upstream HTTP status (0 while unknown).
	statusCode int
	// stopReason is the completion's finish/stop reason (from choices or stream).
	stopReason string
	// nMessages / nToolUse are the request-side counts for the summary columns.
	nMessages int
	nToolUse  int
}

// newTelemetryAcc starts an accumulator at request entry. settings is read on
// each capture call so trace capture + the body cap are hot-reloadable.
func newTelemetryAcc(modelRequested string, stream bool, requestID, sessionID, traceID string, settings func() *config.GatewaySettings) *telemetryAcc {
	return &telemetryAcc{
		start:          time.Now(),
		modelRequested: modelRequested,
		stream:         stream,
		requestID:      requestID,
		sessionID:      sessionID,
		traceID:        traceID,
		settings:       settings,
	}
}

// traceSettings returns the current trace settings, tolerating a nil settings
// source (nil → capture off, uncapped).
func (a *telemetryAcc) traceSettings() (enabled bool, maxBodyBytes int) {
	if a.settings == nil {
		return false, 0
	}
	s := a.settings()
	if s == nil {
		return false, 0
	}
	return s.Trace.CapturePayloadEnabled, s.Trace.MaxBodyKB * 1024
}

// setResult records the dispatcher's routing-layer result (hit provider,
// resolved model, fallback/retry facts) and the observed usage for the emit.
func (a *telemetryAcc) setResult(dr DispatchResult, usage *adapter.Usage) {
	a.provider = dr.Provider
	a.modelResolved = dr.ModelResolved
	a.fallback = dr.Fallback
	a.retryCount = dr.RetryCount
	a.upstreamRequestID = dr.UpstreamRequestID
	a.usage = usage
}

// captureRequest accumulates the request-side trace payload (the raw client body
// and the normalized message array) for the trace ledger. Best-effort: a marshal
// failure leaves the field empty and is ignored. A no-op (zero allocation) when
// capture is disabled in the current settings (hot-reloadable).
func (a *telemetryAcc) captureRequest(rawBody []byte, req *adapter.UnifiedRequest) {
	enabled, maxBodyBytes := a.traceSettings()
	if !enabled {
		return
	}
	a.tracePL.requestRaw = append([]byte(nil), capBytes(rawBody, maxBodyBytes)...)
	if req != nil {
		// Always emit a valid JSON array, even on marshal failure or empty
		// messages — downstream stores this as json.RawMessage and an empty
		// value would surface as invalid JSON in the desktop trace viewer.
		// (json.Marshal of a nil []Message returns "null", so guard the empty
		// case explicitly.)
		var msgs []byte
		if len(req.Messages) == 0 {
			msgs = []byte("[]")
		} else if b, err := json.Marshal(req.Messages); err == nil {
			msgs = b
		} else {
			msgs = []byte("[]")
		}
		a.tracePL.messages = msgs
		a.tracePL.nMessages = len(req.Messages)
		nTools := 0
		for _, m := range req.Messages {
			nTools += len(m.ToolCalls)
		}
		a.tracePL.nToolUse = nTools
	}
}

// captureResponse accumulates the upstream response body for the trace ledger
// (non-streaming). resp.Raw is the byte-for-byte upstream body (ADR-0032); the
// finish reason is taken from the first choice. A no-op when capture is disabled.
func (a *telemetryAcc) captureResponse(resp *adapter.UnifiedResponse, statusCode int) {
	enabled, maxBodyBytes := a.traceSettings()
	if !enabled {
		return
	}
	if resp != nil {
		a.tracePL.responseRaw = append([]byte(nil), capBytes(resp.Raw, maxBodyBytes)...)
		if len(resp.Choices) > 0 {
			a.tracePL.stopReason = resp.Choices[0].FinishReason
		}
	}
	a.tracePL.statusCode = statusCode
}

// captureStreamChunk accumulates the reassembled streaming response for the trace
// ledger. frame is one complete SSE wire frame (the exact bytes sent to the
// client, including the `data: ` prefix and `\n\n` delimiter), so response_raw
// reconstructs the full transcript. The final finish reason is recorded when seen.
// A no-op when capture is disabled. The running transcript is capped at
// maxBodyBytes (a long stream otherwise grows unbounded).
func (a *telemetryAcc) captureStreamChunk(frame []byte, finishReason string) {
	enabled, maxBodyBytes := a.traceSettings()
	if !enabled || len(frame) == 0 {
		return
	}
	// Honor the body cap on the running transcript: stop appending once it has
	// reached maxBodyBytes (0 = uncapped).
	if maxBodyBytes > 0 && len(a.tracePL.responseRaw) >= maxBodyBytes {
		return
	}
	room := frame
	if maxBodyBytes > 0 {
		remaining := maxBodyBytes - len(a.tracePL.responseRaw)
		if remaining < len(frame) {
			room = frame[:remaining]
		}
	}
	a.tracePL.responseRaw = append(a.tracePL.responseRaw, room...)
	if finishReason != "" {
		a.tracePL.stopReason = finishReason
	}
	a.tracePL.statusCode = 200 // stream headers already sent (200) before chunks
}

// captureError records the upstream error body for the trace ledger. A no-op when
// capture is disabled. errBody is the raw upstream response body (capped at
// maxBodyBytes), not the gateway-wrapped error string.
func (a *telemetryAcc) captureError(statusCode int, errBody []byte) {
	enabled, maxBodyBytes := a.traceSettings()
	if !enabled {
		return
	}
	a.tracePL.errorRaw = string(capBytes(errBody, maxBodyBytes))
	a.tracePL.statusCode = statusCode
}

// capBytes returns b truncated to at most n bytes (0 or negative n = no cap).
func capBytes(b []byte, n int) []byte {
	if n <= 0 || len(b) <= n {
		return b
	}
	return b[:n]
}

// emit fans out the accumulated fields once: to OTel (trace span + metrics) and,
// when a recorder is configured, to the request-audit ledger (request_logs). When
// a trace-payload recorder is configured (capture enabled, ADR-0039), it also
// fans out the message + raw layers to the trace_payloads ledger. Called from a
// single deferred site in the handler so all exit paths are covered.
func (a *telemetryAcc) emit(ctx context.Context, pc *plugin.Context, audit observability.RequestLogRecorder, tracePL observability.TracePayloadRecorder) {
	var pt, ct, tt int
	var cachedPrompt int
	cacheHit := false
	cacheTier := ""
	cacheSource := ""
	if a.usage != nil {
		pt, ct, tt = a.usage.PromptTokens, a.usage.CompletionTokens, a.usage.TotalTokens
		cachedPrompt = a.usage.CachedPromptTokens
		if cachedPrompt > 0 {
			cacheHit = true
			cacheTier = "upstream"
			cacheSource = a.provider
		}
	}
	tenant, group, keyID := "", "", ""
	blockedBy := a.blockedBy
	if pc != nil {
		tenant, group, keyID = pc.Tenant, pc.Group, pc.APIKeyID
		if blockedBy == "" {
			blockedBy = pc.BlockedBy
		}
	}
	dur := time.Since(a.start)

	observability.RecordTelemetry(ctx, observability.RequestTelemetry{
		Tenant:             tenant,
		Group:              group,
		APIKeyID:           keyID,
		ModelRequested:     a.modelRequested,
		ModelResolved:      a.modelResolved,
		Provider:           a.provider,
		Stream:             a.stream,
		PromptTokens:       pt,
		CompletionTokens:   ct,
		TotalTokens:        tt,
		TTFT:               a.ttft,
		Duration:           dur,
		CacheHit:           cacheHit,
		CachedPromptTokens: cachedPrompt,
		CacheTier:          cacheTier,
		CacheSource:        cacheSource,
		BlockedBy:          blockedBy,
		RetryCount:         a.retryCount,
		Fallback:           a.fallback,
		ErrorType:          a.errType,
		ErrorDetail:        a.errMsg,
		RequestID:          a.requestID,
		UpstreamRequestID:  a.upstreamRequestID,
		SessionID:          a.sessionID,
		TraceID:            a.traceID,
		SessionSource:      a.sessionSource,
		AgentType:          a.agentType,
		IngressProtocol:    a.ingressProtocol,
	})

	if audit != nil {
		audit.Record(ctx, observability.RequestLog{
			Tenant:             tenant,
			Group:              group,
			APIKeyID:           keyID,
			Provider:           a.provider,
			ModelRequested:     a.modelRequested,
			ModelResolved:      a.modelResolved,
			Stream:             a.stream,
			PromptTokens:       pt,
			CompletionTokens:   ct,
			TotalTokens:        tt,
			TTFTms:             int(a.ttft.Milliseconds()),
			Durationms:         int(dur.Milliseconds()),
			ErrorType:          a.errType,
			BlockedBy:          blockedBy,
			Fallback:           a.fallback,
			CacheHit:           cacheHit,
			CachedPromptTokens: cachedPrompt,
			CacheTier:          cacheTier,
			CacheSource:        cacheSource,
			RequestID:          a.requestID,
			UpstreamRequestID:  a.upstreamRequestID,
			SessionID:          a.sessionID,
			TraceID:            a.traceID,
			SessionSource:      a.sessionSource,
			AgentType:          a.agentType,
			IngressProtocol:    a.ingressProtocol,
		})
	}

	// Trace-payload ledger (ADR-0039): fan out the message + raw layers. Only
	// emitted when a real recorder is configured (capture enabled). A request_id
	// is required to join the row back to request_logs; without one the row is
	// unjoinable and dropped.
	if tracePL != nil && a.requestID != "" {
		tracePL.Record(ctx, observability.TracePayload{
			RequestID:      a.requestID,
			SessionID:      a.sessionID,
			TraceID:        a.traceID,
			Tenant:         tenant,
			Group:          group,
			APIKeyID:       keyID,
			Provider:       a.provider,
			ModelRequested: a.modelRequested,
			Stream:         a.stream,
			AgentType:      a.agentType,
			IngressProtocol: a.ingressProtocol,
			StatusCode:     a.tracePL.statusCode,
			StopReason:     a.tracePL.stopReason,
			NMessages:      a.tracePL.nMessages,
			NToolUse:       a.tracePL.nToolUse,
			Messages:       a.tracePL.messages,
			RequestRaw:     a.tracePL.requestRaw,
			ResponseRaw:    string(a.tracePL.responseRaw),
			ErrorRaw:       a.tracePL.errorRaw,
		})
	}

	// Count requests whose client-supplied request-id was a nil/zero value that
	// the gateway regenerated (Part 1: request-id fix). Labeled by agent_type +
	// tenant so operators can see which agent emits the bad value. The flag is
	// set by requestIDMiddleware on the request ctx; agent_type is detected in
	// the handler (Part 2). Both labels are low-cardinality.
	if _, ok := invalidRequestIDFrom(ctx); ok {
		observability.RecordRequestIDInvalid(ctx, a.agentType, tenant)
	}
}
