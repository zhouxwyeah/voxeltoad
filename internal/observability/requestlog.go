package observability

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// RequestLog is one row of the data-plane request audit ledger (the request_logs
// table). It is a business/compliance record — written 100%, unlike the sampled
// trace/metric telemetry — but still carries NO prompt/completion bodies or raw
// credentials (design/observability.md §日志约定). Fields mirror
// RequestTelemetry; the two are assembled together at the request's end.
type RequestLog struct {
	Tenant   string
	Group    string
	APIKeyID string

	Provider       string
	ModelRequested string
	ModelResolved  string
	Stream         bool

	PromptTokens     int
	CompletionTokens int
	TotalTokens      int

	TTFTms     int
	Durationms int

	ErrorType string
	BlockedBy string
	Fallback  bool

	// CacheHit records whether the upstream prompt cache was hit
	// (CachedPromptTokens > 0). CachedPromptTokens is the cache-read portion of
	// PromptTokens; CacheTier/CacheSource describe the cache origin
	// ("upstream"/<provider>). Mirrors RequestTelemetry.
	CacheHit           bool
	CachedPromptTokens int
	CacheTier          string
	CacheSource        string

	RequestID string // gateway-assigned per-request correlation id (ADR-0021 §5; ADR-0050: always gateway-generated, never the client value)
	// ClientRequestID is the client-supplied X-Request-Id header value (verbatim
	// after trim). Preserved for cross-system correlation; empty when the client
	// sent no header. ADR-0050.
	ClientRequestID string
	SessionID       string // client-supplied session key (X-Voxeltoad-Session header)
	TraceID         string // W3C trace id from traceparent (empty if absent/invalid)
	// UpstreamRequestID is the provider-assigned request id returned in the
	// upstream response (OpenAI x-request-id header, Anthropic request-id
	// header/body, …). Final/successful attempt only. Empty when the provider
	// returned no id or when no upstream call succeeded. Mirrors RequestTelemetry.
	UpstreamRequestID string
	// SessionSource records which mechanism supplied SessionID (header-config,
	// header-generic, body-session, body-metadata, body-user, prefix, or "").
	SessionSource string
	// AgentType is the detected calling agent/client (claude-code, codex,
	// codebuddy, workbuddy, opencode, …). "" when unrecognized (a plain OpenAI
	// SDK/curl/browser). Drives agent-level filtering in the trace UI.
	AgentType string
	// IngressProtocol records which client wire protocol served the request
	// ("openai" / "anthropic"). "" for pre-migration rows (pre-ADR-0045). Used
	// by the management UI for protocol filtering and the passthrough/translated
	// badge (compared against the hit provider's adapter). Mirrors the OTel
	// span attribute llm.ingress.protocol (ADR-0045/0046).
	IngressProtocol string

	CreatedAt time.Time
}

// RequestLogSink is the durable backend the async recorder flushes to (the PG
// request_logs repository in production). Off the request hot path.
type RequestLogSink interface {
	Record(ctx context.Context, r RequestLog) error
}

// RequestLogRecorder enqueues request-audit rows. The proxy depends on this
// narrow interface; the async implementation below is fail-open.
type RequestLogRecorder interface {
	Record(ctx context.Context, r RequestLog)
}

// AsyncRequestLogRecorder is a fail-open RequestLogRecorder mirroring billing's
// AsyncRecorder (ADR-0016): Record enqueues onto a bounded buffer and never
// blocks the request path; a worker drains it to the sink. When the buffer is
// full (sink down/slow) rows are dropped and counted. A dropped audit row is
// acceptable — it is a ledger, not the money path — and never worth adding
// latency to a user request.
type AsyncRequestLogRecorder struct {
	sink    RequestLogSink
	buf     chan RequestLog
	dropped atomic.Int64

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

// NewAsyncRequestLogRecorder builds a recorder over sink with the given buffer
// capacity. Call Start to launch the worker and Close to drain and stop it.
func NewAsyncRequestLogRecorder(sink RequestLogSink, bufferSize int) *AsyncRequestLogRecorder {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &AsyncRequestLogRecorder{
		sink: sink,
		buf:  make(chan RequestLog, bufferSize),
		done: make(chan struct{}),
	}
}

// Start launches the background flush worker (idempotent).
func (a *AsyncRequestLogRecorder) Start() {
	a.startOnce.Do(func() { go a.run() })
}

// Record enqueues a row without blocking; drops (and counts) when the buffer is
// full (fail-open).
func (a *AsyncRequestLogRecorder) Record(_ context.Context, r RequestLog) {
	select {
	case a.buf <- r:
	default:
		n := a.dropped.Add(1)
		if n == 1 {
			Logger().Warn("request log dropped (buffer full)", "dropped_total", n)
		}
		RecordRequestLogDropped(context.Background(), 1)
	}
}

// Dropped returns the number of rows dropped (buffer full or sink error).
func (a *AsyncRequestLogRecorder) Dropped() int64 { return a.dropped.Load() }

// Close stops accepting new rows, drains the buffer, and waits for the worker to
// exit (idempotent).
func (a *AsyncRequestLogRecorder) Close() error {
	a.closeOnce.Do(func() { close(a.buf) })
	<-a.done
	return nil
}

func (a *AsyncRequestLogRecorder) run() {
	defer close(a.done)
	for r := range a.buf {
		if err := a.sink.Record(context.Background(), r); err != nil {
			a.dropped.Add(1) // fail-open: count and move on
			RecordRequestLogDropped(context.Background(), 1)
		}
	}
}
