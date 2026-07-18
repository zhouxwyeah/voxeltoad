package observability

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// TracePayload is one row of the LLM trace-payload ledger (the trace_payloads
// table, ADR-0039). Unlike RequestLog — which carries ONLY non-sensitive metadata
// — TracePayload stores the prompt/completion plaintext and the raw upstream
// request/response bodies. It is the bottom two layers of the 4-layer trace model
// (Session → Request → Messages → Raw), joined 1:1 to request_logs on RequestID.
//
// Capture is opt-in (trace.capture_payload.enabled) and written fail-open async.
// messages and request_raw are kept as json.RawMessage so the pgx driver encodes
// them as JSONB and the recorder never re-parses provider-heterogeneous payloads.
// response_raw is the verbatim upstream response body (for streaming this is the
// SSE transcript, which is not JSON), so it is stored as TEXT.
type TracePayload struct {
	// Correlation + identity (mirror RequestLog so the row is self-describing
	// without a mandatory join, and so session/tenant queries hit this table's
	// own indexes).
	RequestID string
	SessionID string
	TraceID   string
	Tenant    string
	Group     string
	APIKeyID  string

	Provider       string
	ModelRequested string
	Stream         bool

	// AgentType is the detected calling agent/client (claude-code, codex, …).
	// "" when unrecognized. Surfaced as a summary dimension so the request-list
	// view can render it without decoding the JSONB bodies.
	AgentType string

	// Summary dimensions surfaced per event so the request-list view can render
	// a row WITHOUT decoding the large JSONB bodies (mirror reference trace
	// systems).
	StatusCode int
	StopReason string
	NMessages  int
	NToolUse   int
	CreatedAt  time.Time

	// The 4-layer model's bottom two layers. messages is the normalized
	// adapter.Message[] the gateway routed on; request_raw is the original client
	// request body; response_raw is the verbatim upstream response body (TEXT,
	// because streaming responses are SSE transcripts, not JSON); error_raw is the
	// upstream error body on failure. Empty slices are stored as '[]'/'{}' by the
	// sink's defaults; an empty response_raw is stored as ''.
	Messages    json.RawMessage
	RequestRaw  json.RawMessage
	ResponseRaw string
	ErrorRaw    string
}

// TracePayloadSink is the durable backend the async trace-payload recorder flushes
// to (the PG trace_payloads repository in production). Off the request hot path.
type TracePayloadSink interface {
	Record(ctx context.Context, p TracePayload) error
}

// TracePayloadRecorder enqueues trace-payload rows. The proxy depends on this
// narrow interface; the async implementation below is fail-open. A nil recorder
// (capture disabled) means the proxy skips capture entirely.
type TracePayloadRecorder interface {
	Record(ctx context.Context, p TracePayload)
}

// noopTracePayloadRecorder is returned when capture is disabled, so the proxy can
// call Record unconditionally without a nil check at every capture site. It is a
// zero-cost drop.
type noopTracePayloadRecorder struct{}

func (noopTracePayloadRecorder) Record(context.Context, TracePayload) {}

// NoopTracePayloadRecorder is the disabled-capture recorder. Capturing code may
// compare against it (by behavior, not identity) when it wants to skip the
// payload-accumulation work entirely.
var NoopTracePayloadRecorder TracePayloadRecorder = noopTracePayloadRecorder{}

// AsyncTracePayloadRecorder is a fail-open TracePayloadRecorder mirroring
// AsyncRequestLogRecorder (ADR-0016): Record enqueues onto a bounded buffer and
// never blocks the request path; a worker drains it to the sink. When the buffer
// is full (sink down/slow) rows are dropped and counted. A dropped trace row is
// acceptable — trace payloads are debugging state, never the money path.
type AsyncTracePayloadRecorder struct {
	sink    TracePayloadSink
	buf     chan TracePayload
	dropped atomic.Int64

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

// NewAsyncTracePayloadRecorder builds a recorder over sink with the given buffer
// capacity. Call Start to launch the worker and Close to drain and stop it.
func NewAsyncTracePayloadRecorder(sink TracePayloadSink, bufferSize int) *AsyncTracePayloadRecorder {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &AsyncTracePayloadRecorder{
		sink: sink,
		buf:  make(chan TracePayload, bufferSize),
		done: make(chan struct{}),
	}
}

// Start launches the background flush worker (idempotent).
func (a *AsyncTracePayloadRecorder) Start() {
	a.startOnce.Do(func() { go a.run() })
}

// Record enqueues a row without blocking; drops (and counts) when the buffer is
// full (fail-open).
func (a *AsyncTracePayloadRecorder) Record(_ context.Context, p TracePayload) {
	select {
	case a.buf <- p:
	default:
		n := a.dropped.Add(1)
		if n == 1 {
			Logger().Warn("trace payload dropped (buffer full)", "dropped_total", n)
		}
		RecordTracePayloadDropped(context.Background(), 1)
	}
}

// Dropped returns the number of rows dropped (buffer full or sink error).
func (a *AsyncTracePayloadRecorder) Dropped() int64 { return a.dropped.Load() }

// Close stops accepting new rows, drains the buffer, and waits for the worker to
// exit (idempotent).
func (a *AsyncTracePayloadRecorder) Close() error {
	a.closeOnce.Do(func() { close(a.buf) })
	<-a.done
	return nil
}

func (a *AsyncTracePayloadRecorder) run() {
	defer close(a.done)
	for p := range a.buf {
		if err := a.sink.Record(context.Background(), p); err != nil {
			a.dropped.Add(1) // fail-open: count and move on
			RecordTracePayloadDropped(context.Background(), 1)
		}
	}
}
