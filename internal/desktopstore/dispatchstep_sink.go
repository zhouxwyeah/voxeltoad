package desktopstore

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"voxeltoad/internal/observability"
	"voxeltoad/internal/proxy"
)

// stepItem carries a requestID alongside its DispatchStepEvent through the
// buffer channel (the event struct itself does not carry requestID).
type stepItem struct {
	requestID string
	step      proxy.DispatchStepEvent
}

// DispatchStepSink is the desktop SQLite implementation of
// proxy.DispatchObserver (ADR-0057). It buffers events on a bounded channel
// and flushes them asynchronously (fail-open, mirroring AsyncRequestLogRecorder).
type DispatchStepSink struct {
	db        *DB
	buf       chan stepItem
	dropped   atomic.Int64
	done      chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
}

// NewDispatchStepSink builds a SQLite-backed DispatchObserver with the given
// buffer capacity. Call Start to launch the flush worker and Close to drain.
func NewDispatchStepSink(db *DB, bufferSize int) *DispatchStepSink {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &DispatchStepSink{
		db:   db,
		buf:  make(chan stepItem, bufferSize),
		done: make(chan struct{}),
	}
}

// Start launches the background flush worker (idempotent).
func (s *DispatchStepSink) Start() {
	s.startOnce.Do(func() { go s.run() })
}

// Close stops accepting new events, drains the buffer, and waits for the
// worker to exit (idempotent).
func (s *DispatchStepSink) Close() error {
	s.stopOnce.Do(func() { close(s.buf) })
	<-s.done
	return nil
}

// OnDispatchStep implements proxy.DispatchObserver. It enqueues the event
// without blocking; drops (and counts) when the buffer is full (fail-open).
func (s *DispatchStepSink) OnDispatchStep(_ context.Context, requestID string, step proxy.DispatchStepEvent) {
	item := stepItem{requestID: requestID, step: step}
	select {
	case s.buf <- item:
	default:
		n := s.dropped.Add(1)
		if n == 1 {
			observability.Logger().Warn("dispatch step dropped (buffer full)", "dropped_total", n)
		}
	}
}

// Dropped returns the number of events dropped (buffer full or sink error).
func (s *DispatchStepSink) Dropped() int64 { return s.dropped.Load() }

func (s *DispatchStepSink) run() {
	defer close(s.done)
	for item := range s.buf {
		row := DispatchStepRow{
			RequestID:         item.requestID,
			Ordinal:           item.step.Ordinal,
			Provider:          item.step.Provider,
			Endpoint:          item.step.Endpoint,
			Action:            item.step.Action,
			SkipReason:        item.step.SkipReason,
			SelectionOutcome:  item.step.SelectionOutcome,
			ErrorType:         item.step.ErrorType,
			UpstreamRequestID: item.step.UpstreamRequestID,
			DurationMs:        item.step.DurationMs,
			CreatedAt:         time.Now(),
		}
		if err := s.db.Create(&row).Error; err != nil {
			s.dropped.Add(1) // fail-open: count and move on
		}
	}
}
