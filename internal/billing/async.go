package billing

import (
	"context"
	"sync"
	"sync/atomic"

	"voxeltoad/internal/observability"
)

// UsageSink is the durable backend the async recorder flushes to (the PG
// usage_records repository in production). It is separate from QuotaStore: the
// money has already moved synchronously via QuotaStore.Settle, so this path is
// fail-open and may lag/lose under outage (ADR-0012/0016).
type UsageSink interface {
	Record(ctx context.Context, rec UsageRecord) error
}

// AsyncRecorder is a fail-open UsageRecorder: Record enqueues onto a bounded
// buffer and never blocks the request path; a worker goroutine drains the buffer
// to the sink. When the buffer is full (sink down/slow) records are dropped and
// counted, never blocking (ADR-0016). It is a UsageRecorder, so the billing
// plugin uses it transparently.
type AsyncRecorder struct {
	sink    UsageSink
	buf     chan UsageRecord
	dropped atomic.Int64

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{} // closed when the worker has fully drained and exited
}

// NewAsyncRecorder builds a recorder over sink with the given buffer capacity.
// Call Start to launch the worker and Close to drain and stop it.
func NewAsyncRecorder(sink UsageSink, bufferSize int) *AsyncRecorder {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &AsyncRecorder{
		sink: sink,
		buf:  make(chan UsageRecord, bufferSize),
		done: make(chan struct{}),
	}
}

// Start launches the background flush worker (idempotent).
func (a *AsyncRecorder) Start() {
	a.startOnce.Do(func() {
		go a.run()
	})
}

// Record enqueues a usage record without blocking. If the buffer is full the
// record is dropped and the dropped counter incremented (fail-open) — the money
// path already settled via QuotaStore, so a lost audit row is acceptable.
func (a *AsyncRecorder) Record(_ context.Context, rec UsageRecord) error {
	select {
	case a.buf <- rec:
	default:
		n := a.dropped.Add(1)
		// Log sparsely-ish: every drop is cheap to count; emit on the first and
		// then it is visible via Dropped()/metrics. Keep it simple here.
		if n == 1 {
			observability.Logger().Warn("usage record dropped (buffer full)", "dropped_total", n)
		}
	}
	return nil
}

// Dropped returns the number of records dropped due to a full buffer.
func (a *AsyncRecorder) Dropped() int64 { return a.dropped.Load() }

// Close stops accepting new records, drains the buffer to the sink, and waits
// for the worker to exit (idempotent).
func (a *AsyncRecorder) Close() error {
	a.closeOnce.Do(func() {
		close(a.buf)
	})
	<-a.done
	return nil
}

// run drains the buffer to the sink until the buffer is closed and empty.
func (a *AsyncRecorder) run() {
	defer close(a.done)
	for rec := range a.buf {
		if err := a.sink.Record(context.Background(), rec); err != nil {
			// Sink write failed: fail-open, count as dropped and move on (the
			// request path is unaffected; quota already settled).
			a.dropped.Add(1)
		}
	}
}
