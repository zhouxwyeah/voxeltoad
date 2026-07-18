package observability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// capturingSink records everything it receives; optionally errors.
type capturingSink struct {
	mu      sync.Mutex
	got     []RequestLog
	failAll bool
}

func (s *capturingSink) Record(_ context.Context, r RequestLog) error {
	if s.failAll {
		return errors.New("sink down")
	}
	s.mu.Lock()
	s.got = append(s.got, r)
	s.mu.Unlock()
	return nil
}

func (s *capturingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func TestAsyncRequestLogRecorder_FlushesToSink(t *testing.T) {
	sink := &capturingSink{}
	rec := NewAsyncRequestLogRecorder(sink, 8)
	rec.Start()

	for i := 0; i < 3; i++ {
		rec.Record(context.Background(), RequestLog{Tenant: "acme", Provider: "openai"})
	}
	if err := rec.Close(); err != nil { // drains then stops
		t.Fatalf("close: %v", err)
	}
	if sink.count() != 3 {
		t.Errorf("sink received %d records, want 3", sink.count())
	}
}

func TestAsyncRequestLogRecorder_DropsWhenFull(t *testing.T) {
	// A blocked sink + tiny buffer forces drops; Record must never block.
	block := make(chan struct{})
	sink := &blockingSink{release: block}
	rec := NewAsyncRequestLogRecorder(sink, 1)
	rec.Start()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			rec.Record(context.Background(), RequestLog{Tenant: "acme"})
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
		t.Error("expected some records to be dropped when buffer full")
	}
}

func TestAsyncRequestLogRecorder_SinkErrorCountsDropped(t *testing.T) {
	sink := &capturingSink{failAll: true}
	rec := NewAsyncRequestLogRecorder(sink, 8)
	rec.Start()
	rec.Record(context.Background(), RequestLog{Tenant: "acme"})
	_ = rec.Close()
	if rec.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1 (sink error is fail-open)", rec.Dropped())
	}
}

// blockingSink blocks in Record until release is closed, to simulate a slow sink.
type blockingSink struct{ release chan struct{} }

func (b *blockingSink) Record(_ context.Context, _ RequestLog) error {
	<-b.release
	return nil
}
