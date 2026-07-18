package sse

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// Done is the SSE data payload OpenAI-compatible providers send to mark the end
// of a stream: `data: [DONE]`. It is surfaced as a normal Event (Data == Done)
// rather than as io.EOF, so callers can distinguish a clean upstream
// termination from a truncated/dropped stream.
const Done = "[DONE]"

// Event is one decoded Server-Sent Event. Only the fields relevant to the
// gateway are modeled. For multi-line payloads, Data joins the `data:` lines
// with "\n" (per the SSE spec).
type Event struct {
	// Event is the optional `event:` field (e.g. Claude's event types).
	Event string
	// ID is the optional `id:` field.
	ID string
	// Data is the concatenated `data:` payload.
	Data string
}

// Decoder reads an SSE byte stream and yields Events. It correctly reassembles
// frames that span multiple underlying reads (see design/e2e.md Pitfalls:
// half/joined packets).
type Decoder struct {
	sc *bufio.Scanner

	// current frame accumulator
	ev      string
	id      string
	data    []string
	hasData bool
}

// NewDecoder returns a Decoder reading from r.
func NewDecoder(r io.Reader) *Decoder {
	sc := bufio.NewScanner(r)
	// Allow large SSE frames (default 64KB token limit is too small for big
	// completion chunks).
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Decoder{sc: sc}
}

// Next returns the next Event, or io.EOF when the stream is exhausted. A
// dispatched event requires at least one `data:` line; field-only or
// comment-only blocks are skipped.
func (d *Decoder) Next() (Event, error) {
	for d.sc.Scan() {
		line := stripCR(d.sc.Text())

		if line == "" {
			// Blank line dispatches the current frame, if any data was seen.
			if d.hasData {
				ev := d.assemble()
				d.reset()
				return ev, nil
			}
			// Blank line with no pending data: ignore (keep-alive spacing).
			d.reset()
			continue
		}

		// Comment line: ":" prefix. Ignored.
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value := splitField(line)
		switch field {
		case "event":
			d.ev = value
		case "id":
			d.id = value
		case "data":
			d.data = append(d.data, value)
			d.hasData = true
		default:
			// Unknown field: ignore per spec.
		}
	}

	if err := d.sc.Err(); err != nil {
		return Event{}, err
	}

	// Stream ended. Emit a trailing frame that had data but no closing blank
	// line, so a final `data: ...\n` without `\n\n` is not lost.
	if d.hasData {
		ev := d.assemble()
		d.reset()
		return ev, nil
	}
	return Event{}, io.EOF
}

func (d *Decoder) assemble() Event {
	return Event{Event: d.ev, ID: d.id, Data: strings.Join(d.data, "\n")}
}

func (d *Decoder) reset() {
	d.ev = ""
	d.id = ""
	d.data = nil
	d.hasData = false
}

// splitField splits an SSE line into field name and value. Per the spec, the
// value has a single optional leading space removed after the colon.
func splitField(line string) (field, value string) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		// A line with no colon is a field name with an empty value.
		return line, ""
	}
	field = line[:i]
	value = line[i+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value
}

func stripCR(s string) string {
	return strings.TrimSuffix(s, "\r")
}

// Encode serializes an Event into SSE wire bytes, terminated by a blank line.
// Multi-line Data is split into multiple `data:` lines (the inverse of how the
// Decoder joins them), so Encode∘Decode round-trips.
func Encode(e Event) []byte {
	var b bytes.Buffer
	if e.Event != "" {
		b.WriteString("event: ")
		b.WriteString(e.Event)
		b.WriteByte('\n')
	}
	if e.ID != "" {
		b.WriteString("id: ")
		b.WriteString(e.ID)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(e.Data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}
