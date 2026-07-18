package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestRequestIDMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		header     string // X-Request-Id value; "" omits the header
		wantValid  bool   // expect a non-empty, non-zero id in ctx
		wantFlag   bool   // expect invalidRequestID flag on ctx
		wantHeader string // expected normalized header value forwarded to chi ("" => chi-generated)
	}{
		{
			name:       "no header => chi-generated, no flag",
			header:     "",
			wantValid:  true,
			wantFlag:   false,
			wantHeader: "",
		},
		{
			name:       "valid header passes through",
			header:     "abc-123-valid",
			wantValid:  true,
			wantFlag:   false,
			wantHeader: "abc-123-valid",
		},
		{
			name:       "valid header with spaces trimmed",
			header:     "  req_xyz  ",
			wantValid:  true,
			wantFlag:   false,
			wantHeader: "req_xyz",
		},
		{
			name:       "nil-uuid no dashes => regenerated, flagged",
			header:     "00000000000000000000000000000000",
			wantValid:  true, // a fresh id IS produced
			wantFlag:   true,
			wantHeader: "",
		},
		{
			name:       "nil-uuid dashed => regenerated, flagged",
			header:     "00000000-0000-0000-0000-000000000000",
			wantValid:  true,
			wantFlag:   true,
			wantHeader: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotID string
			var gotFlag bool
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.header != "" {
				req.Header.Set(middleware.RequestIDHeader, tt.header)
			}
			// Track the header chi will see (after middleware normalization).
			var seenHeader string
			handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// By now requestIDMiddleware has (possibly) mutated the header
				// and chi has generated+injected the ctx id. Inspect both.
				seenHeader = r.Header.Get(middleware.RequestIDHeader)
				gotID = middleware.GetReqID(r.Context())
				_, gotFlag = invalidRequestIDFrom(r.Context())
			}))
			handler.ServeHTTP(rec, req)

			if tt.wantValid && gotID == "" {
				t.Errorf("expected a non-empty request id, got empty")
			}
			// A regenerated id must differ from the nil-uuid input.
			if tt.wantFlag && gotID == tt.header {
				t.Errorf("regenerated id should differ from rejected input %q, got %q", tt.header, gotID)
			}
			if gotFlag != tt.wantFlag {
				t.Errorf("invalidRequestID flag = %v, want %v", gotFlag, tt.wantFlag)
			}
			// When the header was nil/uuid, the middleware must have cleared it
			// before delegating to chi (so chi generates its own).
			if tt.wantFlag && seenHeader != "" {
				t.Errorf("header should have been cleared before chi, got %q", seenHeader)
			}
			// When valid, the (trimmed) header should reach chi verbatim.
			if !tt.wantFlag && tt.wantHeader != "" && seenHeader != tt.wantHeader {
				t.Errorf("header forwarded to chi = %q, want %q", seenHeader, tt.wantHeader)
			}
			_ = gotID
		})
	}
}
