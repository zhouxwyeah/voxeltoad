package sse_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"voxeltoad/pkg/sse"
)

// collect drains a Decoder into a slice, returning the events and the
// terminating error (expected to be io.EOF).
func collect(t *testing.T, r io.Reader) ([]sse.Event, error) {
	t.Helper()
	d := sse.NewDecoder(r)
	var out []sse.Event
	for {
		e, err := d.Next()
		if err != nil {
			return out, err
		}
		out = append(out, e)
	}
}

func TestDecoder_Framing(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []sse.Event
	}{
		{
			name: "single data event",
			in:   "data: hello\n\n",
			want: []sse.Event{{Data: "hello"}},
		},
		{
			name: "two events",
			in:   "data: one\n\ndata: two\n\n",
			want: []sse.Event{{Data: "one"}, {Data: "two"}},
		},
		{
			name: "multi-line data joined with newline",
			in:   "data: a\ndata: b\n\n",
			want: []sse.Event{{Data: "a\nb"}},
		},
		{
			name: "optional leading space stripped (one only)",
			in:   "data:no-space\n\ndata:  two-spaces\n\n",
			want: []sse.Event{{Data: "no-space"}, {Data: " two-spaces"}},
		},
		{
			name: "event and id fields",
			in:   "event: message\nid: 42\ndata: x\n\n",
			want: []sse.Event{{Event: "message", ID: "42", Data: "x"}},
		},
		{
			name: "comment lines ignored",
			in:   ": keep-alive ping\ndata: y\n\n",
			want: []sse.Event{{Data: "y"}},
		},
		{
			name: "CRLF line endings",
			in:   "data: crlf\r\n\r\n",
			want: []sse.Event{{Data: "crlf"}},
		},
		{
			name: "blank-only block produces no event",
			in:   "\n\ndata: real\n\n",
			want: []sse.Event{{Data: "real"}},
		},
		{
			name: "trailing event without final blank line is still emitted",
			in:   "data: last\n",
			want: []sse.Event{{Data: "last"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := collect(t, strings.NewReader(tt.in))
			if !errors.Is(err, io.EOF) {
				t.Fatalf("terminating err = %v, want io.EOF", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d events %+v, want %d %+v", len(got), got, len(tt.want), tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("event[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDecoder_DoneSentinel(t *testing.T) {
	got, err := collect(t, strings.NewReader("data: chunk\n\ndata: [DONE]\n\n"))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("terminating err = %v, want io.EOF", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[1].Data != sse.Done {
		t.Errorf("last event data = %q, want Done sentinel %q", got[1].Data, sse.Done)
	}
}

// TestDecoder_PartialFramesSpanningReads is the critical correctness case from
// design/e2e.md: a single SSE frame may arrive split across multiple reads.
// Feeding one byte at a time must still assemble identical events.
func TestDecoder_PartialFramesSpanningReads(t *testing.T) {
	const in = "event: message\ndata: split\ndata: across\n\ndata: [DONE]\n\n"
	got, err := collect(t, iotest.OneByteReader(strings.NewReader(in)))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("terminating err = %v, want io.EOF", err)
	}
	want := []sse.Event{
		{Event: "message", Data: "split\nacross"},
		{Data: sse.Done},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		in   sse.Event
		want string
	}{
		{"data only", sse.Event{Data: "hello"}, "data: hello\n\n"},
		{"multi-line data", sse.Event{Data: "a\nb"}, "data: a\ndata: b\n\n"},
		{"with event and id", sse.Event{Event: "message", ID: "7", Data: "x"}, "event: message\nid: 7\ndata: x\n\n"},
		{"done sentinel", sse.Event{Data: sse.Done}, "data: [DONE]\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(sse.Encode(tt.in)); got != tt.want {
				t.Errorf("Encode(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRoundTrip ensures encoding then decoding yields the original events,
// guaranteeing the gateway can faithfully re-emit what it parsed.
func TestRoundTrip(t *testing.T) {
	events := []sse.Event{
		{Event: "message", ID: "1", Data: "first"},
		{Data: "multi\nline"},
		{Data: sse.Done},
	}
	var b strings.Builder
	for _, e := range events {
		b.Write(sse.Encode(e))
	}
	got, err := collect(t, strings.NewReader(b.String()))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("terminating err = %v, want io.EOF", err)
	}
	if len(got) != len(events) {
		t.Fatalf("round-trip got %d events, want %d", len(got), len(events))
	}
	for i := range events {
		if got[i] != events[i] {
			t.Errorf("event[%d] = %+v, want %+v", i, got[i], events[i])
		}
	}
}
