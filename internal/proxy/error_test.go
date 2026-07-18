package proxy

import (
	"errors"
	"testing"
)

func TestUpstreamError_Retryable(t *testing.T) {
	tests := []struct {
		name string
		kind errKind
		want bool
	}{
		{"build error not retryable", errBuild, false},
		{"upstream 4xx not retryable", errUpstream4xx, false},
		{"upstream 5xx retryable", errUpstream5xx, true},
		{"timeout retryable", errTimeout, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &upstreamError{kind: tt.kind, err: errors.New("x")}
			if got := e.Retryable(); got != tt.want {
				t.Errorf("Retryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClassifyUpstreamStatus maps an upstream HTTP status to the right error
// kind: 4xx → errUpstream4xx (non-retryable), 5xx → errUpstream5xx (retryable).
func TestClassifyUpstreamStatus(t *testing.T) {
	tests := []struct {
		status int
		want   errKind
	}{
		{400, errUpstream4xx},
		{401, errUpstream4xx},
		{429, errUpstream4xx},
		{500, errUpstream5xx},
		{502, errUpstream5xx},
		{503, errUpstream5xx},
	}
	for _, tt := range tests {
		if got := classifyUpstreamStatus(tt.status); got != tt.want {
			t.Errorf("classifyUpstreamStatus(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

// mapForwardError must still produce sensible client-facing statuses after the
// split: both 4xx and 5xx upstream failures surface as 502 to the client (the
// gateway failed to get a good answer), timeout as 504, build as 500.
func TestMapForwardError_AfterSplit(t *testing.T) {
	cases := []struct {
		kind       errKind
		wantStatus int
	}{
		{errBuild, 500},
		{errUpstream4xx, 502},
		{errUpstream5xx, 502},
		{errTimeout, 504},
	}
	for _, c := range cases {
		status, _ := mapForwardError(&upstreamError{kind: c.kind, err: errors.New("x")})
		if status != c.wantStatus {
			t.Errorf("kind %v → status %d, want %d", c.kind, status, c.wantStatus)
		}
	}
}
