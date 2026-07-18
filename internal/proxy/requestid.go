package proxy

import (
	"strings"
)

// normalizeRequestID validates a client-supplied request/correlation id (from
// the X-Request-Id / X-Trace-Id headers, or a chi-generated id). It returns the
// trimmed id and ok=true when the value is usable as a correlation key, or
// ("", false) when the value should be treated as absent.
//
// The only rejection today is the "nil UUID" family: a value that is empty,
// whitespace-only, or consists entirely of zeros (optionally with dashes/spaces)
// — e.g. "00000000000000000000000000000000", "00000000-0000-0000-0000-000000000000".
// These are emitted by some agent SDKs / upstream proxies that allocate a UUID
// buffer but never fill it. The gateway treats them as missing so chi generates
// a fresh correlation id instead of propagating an unjoinable zero value through
// the access log, request_logs, trace_payloads, and the OTel span.
//
// Everything else (including non-UUID chi ids like "host/random-000001") is
// accepted as-is; the gateway does not mandate a specific id format.
func normalizeRequestID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", false
	}
	if isAllZeroID(id) {
		return "", false
	}
	return id, true
}

// isAllZeroID reports whether id is a nil/zero value: after dropping dashes and
// spaces, every remaining character is '0'. This catches the 32-hex no-dash
// form, the 8-4-4-4-12 dashed nil UUID, and any padding variant, without
// parsing the UUID (so non-UUID formats are never falsely rejected — a non-zero
// character anywhere makes it valid).
func isAllZeroID(id string) bool {
	hasDigit := false
	for _, c := range id {
		switch c {
		case '-', ' ', '\t':
			continue
		case '0':
			hasDigit = true
		default:
			return false // any non-zero, non-separator char => real id
		}
	}
	return hasDigit // all chars were '0' (and maybe separators); reject iff at least one '0'
}
