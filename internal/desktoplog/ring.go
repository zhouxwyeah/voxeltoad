// Package desktoplog captures the desktop gateway's log output for the UI's
// log viewer: a fixed-capacity in-memory ring (recent lines served over the
// read API) plus an on-disk file with single-generation startup rotation.
//
// Both stdlib `log` (startup/retention lines) and slog access logs
// (observability.Logger) are teed into these sinks by desktopapp.Main via
// log.SetOutput + observability.SetLogOutput.
package desktoplog

import (
	"strings"
	"sync"
)

// Ring is a goroutine-safe fixed-capacity buffer of log lines, implementing
// io.Writer. Writes are split on newlines; a trailing partial line is held
// until the newline arrives (log/slog always terminate lines, so the holdover
// is a corner case, not the norm). When full, the oldest line is dropped.
type Ring struct {
	mu      sync.Mutex
	lines   []string
	cap     int
	pending strings.Builder // partial line not yet newline-terminated
}

// NewRing returns a ring holding at most capacity lines.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 1
	}
	return &Ring{cap: capacity}
}

// Write implements io.Writer. It never fails — logging must not break the
// logged-about code path.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending.Write(p)
	for {
		s := r.pending.String()
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := s[:idx]
		r.pending.Reset()
		r.pending.WriteString(s[idx+1:])
		r.append(line)
	}
	return len(p), nil
}

func (r *Ring) append(line string) {
	if len(r.lines) >= r.cap {
		// Drop the oldest: copy-shift keeps the slice ordered oldest→newest
		// without ring-index bookkeeping; capacity is small (≤ a few thousand).
		copy(r.lines, r.lines[1:])
		r.lines = r.lines[:r.cap-1]
	}
	r.lines = append(r.lines, line)
}

// Tail returns up to n most recent lines, oldest first. A pending partial
// line is not included until terminated.
func (r *Ring) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	out := make([]string, n)
	copy(out, r.lines[len(r.lines)-n:])
	return out
}

// Len reports how many lines are buffered (for tests/diagnostics).
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}
