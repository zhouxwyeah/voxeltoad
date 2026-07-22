package observability

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// RequestTelemetry is the per-request semantic record assembled once at the end
// of a request and fanned out to trace + metric (and, separately, the audit
// sink). Its fields mirror the mandatory llm.* schema in design/observability.md.
//
// It carries ONLY non-sensitive fields: identifiers, model names, token counts,
// timings, and classifications. Prompt/completion bodies and raw credentials
// MUST NOT be placed here (privacy rule, design/observability.md §日志约定).
type RequestTelemetry struct {
	Tenant   string
	Group    string
	APIKeyID string

	ModelRequested string
	ModelResolved  string
	Provider       string
	Stream         bool

	PromptTokens     int
	CompletionTokens int
	TotalTokens      int

	TTFT     time.Duration
	Duration time.Duration

	CacheHit bool
	// CachedPromptTokens is the portion of PromptTokens that hit the upstream
	// prompt cache (OpenAI cached_tokens / Claude cache_read_input_tokens).
	// Zero when no cache hit. Drives the cache-hit billing discount.
	CachedPromptTokens int
	// CacheTier labels where the hit originated: "upstream" for v1 (the only
	// source today); reserved for a future gateway-side response cache ("gateway").
	// Empty on a miss. Observability-only, never a billing dimension.
	CacheTier string
	// CacheSource records the provider that supplied the cache hit (e.g.
	// "openai", "claude"). Empty on a miss. Observability-only.
	CacheSource string
	BlockedBy   string // llm.plugin.blocked_by (empty = not blocked)
	RetryCount  int
	Fallback    bool
	ErrorType   string // OpenAI-compatible error type, empty on success
	// RequestID/SessionID/TraceID are correlation identifiers. They are attached
	// to the trace span (and persisted to the audit ledger) but NEVER used as
	// metric labels — doing so would explode cardinality. Empty when not present.
	RequestID string
	SessionID string
	TraceID   string
	// ClientRequestID is the client-supplied X-Request-Id header value (verbatim
	// after trim). Persisted separately from RequestID because some agents (Claude
	// Code, Codex, …) reuse the same id across every request in a session; the
	// gateway always generates its own RequestID now (ADR-0050). Empty when the
	// client sent no X-Request-Id header.
	ClientRequestID string
	// UpstreamRequestID is the provider-assigned request correlation id
	// returned in the upstream response (OpenAI's x-request-id header,
	// Anthropic's request-id header/body, …). Captured for the final/successful
	// attempt only — per-attempt capture (including failed retries/failovers)
	// is a separate follow-up. Attached to the trace span and persisted to the
	// audit ledger so support/reconciliation can map a gateway request to the
	// provider-side request. Empty when the provider returned no id.
	UpstreamRequestID string
	// SessionSource records which mechanism supplied the session key (header,
	// body, prefix, etc.). Observability only — never a metric label.
	SessionSource string
	// AgentType is the detected calling agent (claude-code, codex, …). Attached
	// to the trace span; never a metric label.
	AgentType string
	// IngressProtocol is the client wire shape that served the request (openai
	// / anthropic). Low-cardinality (2 values), attached to the trace span;
	// never a metric label, NOT persisted to the audit ledger (ADR-0045).
	IngressProtocol string
	// ErrorDetail is the truncated underlying cause (e.g. "upstream returned
	// 500: ..."), for troubleshooting. It is NOT a classification dimension and
	// is only attached to the trace span (never used as a metric label, never
	// persisted to the audit ledger). Bounded to avoid oversized spans.
	ErrorDetail string
}

// llmMetrics holds the OTel instruments. They are (re)built from the global
// MeterProvider by initInstruments, which runs on package init and can be
// re-run by tests that swap the provider.
type llmMetrics struct {
	requests        metric.Int64Counter
	tokens          metric.Int64Counter
	ttft            metric.Float64Histogram
	duration        metric.Float64Histogram
	upstreamErrors  metric.Int64Counter
	cacheHits       metric.Int64Counter
	ratelimitReject metric.Int64Counter
	reqLogsDropped  metric.Int64Counter
	tracePLDropped  metric.Int64Counter
	reqIDInvalid    metric.Int64Counter
	sessIDInvalid   metric.Int64Counter
}

var (
	instrumentsMu sync.RWMutex
	instruments   *llmMetrics
)

func init() { initInstruments() }

// initInstruments builds the metric instruments against the current global
// MeterProvider. Instrument creation errors are ignored (no-op instruments are
// returned), so telemetry never breaks the request path.
func initInstruments() {
	m := otel.Meter("voxeltoad/llm")
	reqs, _ := m.Int64Counter("llm_requests_total")
	toks, _ := m.Int64Counter("llm_tokens_total")
	ttft, _ := m.Float64Histogram("llm_ttft_seconds")
	dur, _ := m.Float64Histogram("llm_request_duration_seconds")
	upErr, _ := m.Int64Counter("llm_upstream_errors_total")
	cache, _ := m.Int64Counter("llm_cache_hits_total")
	rl, _ := m.Int64Counter("llm_ratelimit_rejected_total")
	drp, _ := m.Int64Counter("request_logs_dropped_total")
	tpl, _ := m.Int64Counter("trace_payloads_dropped_total")
	ridInv, _ := m.Int64Counter("request_id_invalid_total")
	sidInv, _ := m.Int64Counter("session_id_invalid_total")

	instrumentsMu.Lock()
	instruments = &llmMetrics{
		requests: reqs, tokens: toks, ttft: ttft, duration: dur,
		upstreamErrors: upErr, cacheHits: cache, ratelimitReject: rl,
		reqLogsDropped: drp, tracePLDropped: tpl, reqIDInvalid: ridInv,
		sessIDInvalid: sidInv,
	}
	instrumentsMu.Unlock()
}

func currentInstruments() *llmMetrics {
	instrumentsMu.RLock()
	defer instrumentsMu.RUnlock()
	return instruments
}

// RecordTelemetry emits the request's semantic fields to the active span and
// the metric instruments. It is safe to call with a context whose span is a
// no-op (SetAttributes is then a no-op) and with no configured exporter (the
// global providers absorb it). It never logs prompt/completion bodies.
func RecordTelemetry(ctx context.Context, t RequestTelemetry) {
	recordSpan(ctx, t)
	recordMetrics(ctx, t)
}

func recordSpan(ctx context.Context, t RequestTelemetry) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(AttrTenant, t.Tenant),
		attribute.String(AttrGroup, t.Group),
		attribute.String(AttrAPIKeyID, t.APIKeyID),
		attribute.String(AttrModelRequested, t.ModelRequested),
		attribute.String(AttrModelResolved, t.ModelResolved),
		attribute.String(AttrProvider, t.Provider),
		attribute.Bool(AttrStream, t.Stream),
		attribute.Int64(AttrTokensPrompt, int64(t.PromptTokens)),
		attribute.Int64(AttrTokensCompletion, int64(t.CompletionTokens)),
		attribute.Int64(AttrTokensTotal, int64(t.TotalTokens)),
		attribute.Int64(AttrTTFTms, t.TTFT.Milliseconds()),
		attribute.Int64(AttrDurationms, t.Duration.Milliseconds()),
		attribute.Bool(AttrCacheHit, t.CacheHit),
		attribute.Int64(AttrTokensCachedPrompt, int64(t.CachedPromptTokens)),
		attribute.String(AttrCacheTier, t.CacheTier),
		attribute.String(AttrCacheSource, t.CacheSource),
		attribute.String(AttrPluginBlockedBy, t.BlockedBy),
		attribute.Int64(AttrRetryCount, int64(t.RetryCount)),
		attribute.Bool(AttrFallback, t.Fallback),
		attribute.String(AttrErrorType, t.ErrorType),
		attribute.String(AttrRequestID, t.RequestID),
		attribute.String(AttrClientRequestID, t.ClientRequestID),
		attribute.String(AttrUpstreamRequestID, t.UpstreamRequestID),
		attribute.String(AttrSessionID, t.SessionID),
		attribute.String(AttrTraceID, t.TraceID),
		attribute.String(AttrSessionSource, t.SessionSource),
		attribute.String(AttrAgentType, t.AgentType),
		attribute.String(AttrIngressProtocol, t.IngressProtocol),
	}
	if t.ErrorDetail != "" {
		attrs = append(attrs, attribute.String(AttrErrorDetail, t.ErrorDetail))
	}
	span.SetAttributes(attrs...)
}

func recordMetrics(ctx context.Context, t RequestTelemetry) {
	m := currentInstruments()
	if m == nil {
		return
	}
	status := "ok"
	if t.ErrorType != "" {
		status = t.ErrorType
	}

	m.requests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tenant", t.Tenant),
		attribute.String("model", t.ModelRequested),
		attribute.String("provider", t.Provider),
		attribute.String("status", status),
	))
	if t.PromptTokens > 0 {
		m.tokens.Add(ctx, int64(t.PromptTokens), metric.WithAttributes(
			attribute.String("tenant", t.Tenant),
			attribute.String("model", t.ModelRequested),
			attribute.String("provider", t.Provider),
			attribute.String("type", "prompt"),
		))
	}
	if t.CompletionTokens > 0 {
		m.tokens.Add(ctx, int64(t.CompletionTokens), metric.WithAttributes(
			attribute.String("tenant", t.Tenant),
			attribute.String("model", t.ModelRequested),
			attribute.String("provider", t.Provider),
			attribute.String("type", "completion"),
		))
	}
	if t.TTFT > 0 {
		m.ttft.Record(ctx, t.TTFT.Seconds(), metric.WithAttributes(
			attribute.String("provider", t.Provider),
		))
	}
	m.duration.Record(ctx, t.Duration.Seconds(), metric.WithAttributes(
		attribute.String("provider", t.Provider),
		attribute.Bool("stream", t.Stream),
	))
	if t.ErrorType == "upstream_error" || t.ErrorType == "timeout_error" {
		m.upstreamErrors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("provider", t.Provider),
			attribute.String("error_type", t.ErrorType),
		))
	}
	if t.CacheHit {
		m.cacheHits.Add(ctx, 1, metric.WithAttributes(
			attribute.String("tenant", t.Tenant),
			attribute.String("model", t.ModelRequested),
			attribute.String("provider", t.Provider),
		))
	}
	if t.ErrorType == "rate_limit_error" {
		m.ratelimitReject.Add(ctx, 1, metric.WithAttributes(attribute.String("tenant", t.Tenant)))
	}
}

// RecordRequestLogDropped increments the request_logs_dropped_total counter by
// n. Call this each time the async request-log buffer drops one or more rows.
func RecordRequestLogDropped(ctx context.Context, n int64) {
	m := currentInstruments()
	if m == nil {
		return
	}
	m.reqLogsDropped.Add(ctx, n)
}

// RecordTracePayloadDropped increments the trace_payloads_dropped_total counter
// by n. Call this each time the async trace-payload buffer drops one or more
// rows (buffer full or sink error). Mirrors RecordRequestLogDropped.
func RecordTracePayloadDropped(ctx context.Context, n int64) {
	m := currentInstruments()
	if m == nil {
		return
	}
	m.tracePLDropped.Add(ctx, n)
}

// RecordRequestIDInvalid increments the request_id_invalid_total counter,
// labeled by the detected agent type and tenant. Call this when a client
// supplies a nil/zero request-id (e.g. "0000...0000") that the gateway rejects
// and regenerates. The agent and tenant labels let operators see which agent is
// emitting the bad value; both are low-cardinality.
func RecordRequestIDInvalid(ctx context.Context, agentType, tenant string) {
	m := currentInstruments()
	if m == nil {
		return
	}
	m.reqIDInvalid.Add(ctx, 1, metric.WithAttributes(
		attribute.String("agent_type", agentType),
		attribute.String("tenant", tenant),
	))
}

// RecordSessionIDInvalid increments the session_id_invalid_total counter,
// labeled by the extraction source that carried the malformed value and the
// tenant. Call this when a client-supplied session id fails validateSessionID
// (too short, too long, or illegal characters) and the extractor falls through
// to a lower-priority source (DEFECT-A). source and tenant are low-cardinality.
func RecordSessionIDInvalid(ctx context.Context, source, tenant string) {
	m := currentInstruments()
	if m == nil {
		return
	}
	m.sessIDInvalid.Add(ctx, 1, metric.WithAttributes(
		attribute.String("source", source),
		attribute.String("tenant", tenant),
	))
}
