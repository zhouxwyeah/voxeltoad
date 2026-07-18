package observability

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// capturingPayloadSink records every TracePayload it receives; optionally errors.
type capturingPayloadSink struct {
	mu      sync.Mutex
	got     []TracePayload
	failAll bool
}

func (s *capturingPayloadSink) Record(_ context.Context, p TracePayload) error {
	if s.failAll {
		return errors.New("sink down")
	}
	s.mu.Lock()
	s.got = append(s.got, p)
	s.mu.Unlock()
	return nil
}

func (s *capturingPayloadSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func TestAsyncTracePayloadRecorder_FlushesToSink(t *testing.T) {
	sink := &capturingPayloadSink{}
	rec := NewAsyncTracePayloadRecorder(sink, 8)
	rec.Start()

	for i := 0; i < 3; i++ {
		rec.Record(context.Background(), TracePayload{
			RequestID: "req-1",
			Messages:  json.RawMessage(`[]`),
		})
	}
	if err := rec.Close(); err != nil { // drains then stops
		t.Fatalf("close: %v", err)
	}
	if sink.count() != 3 {
		t.Errorf("sink received %d payloads, want 3", sink.count())
	}
}

func TestAsyncTracePayloadRecorder_DropsWhenFull(t *testing.T) {
	// A blocked sink + tiny buffer forces drops; Record must never block.
	block := make(chan struct{})
	sink := &blockingPayloadSink{release: block}
	rec := NewAsyncTracePayloadRecorder(sink, 1)
	rec.Start()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			rec.Record(context.Background(), TracePayload{RequestID: "req"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked; expected fail-open non-blocking enqueue")
	}
	close(block)
	_ = rec.Close()
	if rec.Dropped() == 0 {
		t.Error("expected some payloads to be dropped when buffer full")
	}
}

func TestAsyncTracePayloadRecorder_SinkErrorCountsDropped(t *testing.T) {
	sink := &capturingPayloadSink{failAll: true}
	rec := NewAsyncTracePayloadRecorder(sink, 8)
	rec.Start()
	rec.Record(context.Background(), TracePayload{RequestID: "req-1"})
	_ = rec.Close()
	if rec.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1 (sink error is fail-open)", rec.Dropped())
	}
}

// blockingPayloadSink blocks in Record until release is closed.
type blockingPayloadSink struct{ release chan struct{} }

func (b *blockingPayloadSink) Record(_ context.Context, _ TracePayload) error {
	<-b.release
	return nil
}

// noopTracePayloadRecorder is the disabled-capture implementation.
func TestNoopTracePayloadRecorder_DropsNothing(t *testing.T) {
	noop := NoopTracePayloadRecorder
	noop.Record(context.Background(), TracePayload{RequestID: "req"})
	// No panic, no panic, no side effect — the contract is simply "safe to call".
}
