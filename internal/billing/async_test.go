package billing_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"voxeltoad/internal/billing"
)

// countingSink records what the async recorder flushes; optionally blocks to
// simulate a slow/stuck PG so the buffer fills.
type countingSink struct {
	mu      sync.Mutex
	got     []billing.UsageRecord
	release chan struct{} // if non-nil, Record blocks until it is closed
}

func (s *countingSink) Record(_ context.Context, rec billing.UsageRecord) error {
	if s.release != nil {
		<-s.release
	}
	s.mu.Lock()
	s.got = append(s.got, rec)
	s.mu.Unlock()
	return nil
}

func (s *countingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

// Records are flushed asynchronously to the sink; Record itself never blocks.
func TestAsyncRecorder_FlushesToSink(t *testing.T) {
	sink := &countingSink{}
	r := billing.NewAsyncRecorder(sink, 16)
	r.Start()
	defer func() { _ = r.Close() }()

	for i := 0; i < 5; i++ {
		if err := r.Record(context.Background(), billing.UsageRecord{Tenant: "a", Cost: int64(i)}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	// Eventually the sink has all 5.
	waitFor(t, func() bool { return sink.count() == 5 }, time.Second)
	if r.Dropped() != 0 {
		t.Errorf("Dropped = %d, want 0", r.Dropped())
	}
}

// When the sink is stuck and the buffer fills, Record drops (never blocks) and
// increments the Dropped counter (fail-open, ADR-0016).
func TestAsyncRecorder_DropsWhenBufferFull(t *testing.T) {
	sink := &countingSink{release: make(chan struct{})} // blocks forever (until released)
	r := billing.NewAsyncRecorder(sink, 2)              // tiny buffer
	r.Start()

	// Pump many records fast. The worker pulls one (and blocks in the sink), the
	// buffer holds ~2, the rest must be dropped without blocking this goroutine.
	const n = 200
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			_ = r.Record(context.Background(), billing.UsageRecord{Tenant: "a"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked when buffer was full; must be fail-open (non-blocking)")
	}

	if r.Dropped() == 0 {
		t.Error("expected some drops when buffer full, got 0")
	}

	// Release the sink and close; no panic, drains what is buffered.
	close(sink.release)
	_ = r.Close()
}

// Close drains buffered records before returning.
func TestAsyncRecorder_CloseDrains(t *testing.T) {
	sink := &countingSink{}
	r := billing.NewAsyncRecorder(sink, 64)
	r.Start()

	for i := 0; i < 10; i++ {
		_ = r.Record(context.Background(), billing.UsageRecord{Tenant: "a"})
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if sink.count() != 10 {
		t.Errorf("after Close drained = %d, want 10", sink.count())
	}
}

// Start is idempotent: calling it multiple times must not launch extra workers
// (which would cause duplicate flushes or a panic on double-close of done).
func TestAsyncRecorder_StartIdempotent(t *testing.T) {
	sink := &countingSink{}
	r := billing.NewAsyncRecorder(sink, 64)
	r.Start()
	r.Start() // second call is a no-op
	r.Start() // third too

	for i := 0; i < 3; i++ {
		_ = r.Record(context.Background(), billing.UsageRecord{Tenant: "a"})
	}
	_ = r.Close()

	// Exactly 3 flushed, not 3×3=9 (which would happen if 3 workers ran).
	if sink.count() != 3 {
		t.Errorf("idempotent Start: flushed %d, want 3 (no duplicate workers)", sink.count())
	}
}

// Close is idempotent: calling it multiple times must not panic (double close
// of the buf channel) and must return nil each time.
func TestAsyncRecorder_CloseIdempotent(t *testing.T) {
	sink := &countingSink{}
	r := billing.NewAsyncRecorder(sink, 64)
	r.Start()
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("third Close: %v", err)
	}
}

// When the sink returns an error, the worker counts the record as dropped
// (fail-open) and keeps going — never propagating the error to the request path.
func TestAsyncRecorder_SinkErrorCountsAsDropped(t *testing.T) {
	sink := &errorSink{}
	r := billing.NewAsyncRecorder(sink, 16)
	r.Start()

	for i := 0; i < 5; i++ {
		_ = r.Record(context.Background(), billing.UsageRecord{Tenant: "a"})
	}

	// All 5 sink writes fail → all counted as dropped.
	waitFor(t, func() bool { return r.Dropped() == 5 }, time.Second)
	if sink.attempts != 5 {
		t.Errorf("sink attempts = %d, want 5", sink.attempts)
	}
	_ = r.Close()
}

// errorSink always fails — simulates a down PG (fail-open path).
type errorSink struct{ attempts int }

func (s *errorSink) Record(_ context.Context, _ billing.UsageRecord) error {
	s.attempts++
	return errors.New("sink down")
}

// bufferSize < 1 is clamped to 1 so the channel is always usable.
func TestAsyncRecorder_BufferSizeClamped(t *testing.T) {
	r := billing.NewAsyncRecorder(&countingSink{}, 0)
	r.Start()
	// A buffer of size 0 would be unbuffered (always block on first send); size 1
	// lets one record sit without a reader. Record should not block.
	done := make(chan struct{})
	go func() {
		_ = r.Record(context.Background(), billing.UsageRecord{Tenant: "a"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record blocked; buffer size 0 should be clamped to 1")
	}
	_ = r.Close()
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
