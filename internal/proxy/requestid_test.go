package proxy

import "testing"

func TestNormalizeRequestID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"empty", "", "", false},
		{"whitespace only", "   \t ", "", false},
		{"nil UUID no dashes", "00000000000000000000000000000000", "", false},
		{"nil UUID dashed", "00000000-0000-0000-0000-000000000000", "", false},
		{"nil UUID with surrounding spaces", "  00000000000000000000000000000000  ", "", false},
		{"single zero", "0", "", false},
		// "----" has no '0' digit, so isAllZeroID returns false (not a nil-uuid
		// pattern); it is accepted as-is. We only reject the all-zero family.
		{"dashes only no zero", "----", "----", true},

		{"valid chi id", "host/random-000001", "host/random-000001", true},
		{"valid hex id", "0af7651916cd43dd8448eb211c80319c", "0af7651916cd43dd8448eb211c80319c", true},
		{"valid dashed uuid", "12345678-1234-1234-1234-123456789012", "12345678-1234-1234-1234-123456789012", true},
		{"valid with spaces trimmed", "  abc-123  ", "abc-123", true},
		{"OpenAI-style", "req_abc123XYZ", "req_abc123XYZ", true},
		{"id with leading zero but nonzero", "00000000000000000000000000000001", "00000000000000000000000000000001", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeRequestID(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Errorf("normalizeRequestID(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}
