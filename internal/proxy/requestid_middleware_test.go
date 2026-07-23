package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestRequestIDMiddleware(t *testing.T) {
	// ADR-0050: the gateway ALWAYS generates a fresh request_id and never
	// adopts the client-supplied value. The client value is preserved on ctx
	// (clientRequestIDFrom) for separate persistence as client_request_id.
	// The nil-uuid reject path stays (for the labeled warning metric), but it
	// is now a strict subset of "always regenerate" rather than the only
	// regeneration trigger.
	tests := []struct {
		name          string
		header        string // X-Request-Id value; "" omits the header
		wantGenerated bool   // expect a non-empty chi-generated id in ctx
		wantFlag      bool   // expect invalidRequestID flag on ctx (nil-uuid only)
		wantClientID  string // expected client_request_id value on ctx
	}{
		{
			name:          "no header => chi-generated, no client id, no flag",
			header:        "",
			wantGenerated: true,
			wantFlag:      false,
			wantClientID:  "",
		},
		{
			name:          "valid header => regenerated (not adopted), client id preserved, no flag",
			header:        "abc-123-valid",
			wantGenerated: true,
			wantFlag:      false,
			wantClientID:  "abc-123-valid",
		},
		{
			name:          "valid header with spaces => client id trimmed, regenerated, no flag",
			header:        "  req_xyz  ",
			wantGenerated: true,
			wantFlag:      false,
			wantClientID:  "req_xyz",
		},
		{
			name:          "nil-uuid no dashes => regenerated, flagged, client id preserved verbatim",
			header:        "00000000000000000000000000000000",
			wantGenerated: true, // a fresh id IS produced
			wantFlag:      true,
			wantClientID:  "00000000000000000000000000000000",
		},
		{
			name:          "nil-uuid dashed => regenerated, flagged, client id preserved verbatim",
			header:        "00000000-0000-0000-0000-000000000000",
			wantGenerated: true,
			wantFlag:      true,
			wantClientID:  "00000000-0000-0000-0000-000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotID string
			var gotFlag bool
			var gotClientID string
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.header != "" {
				req.Header.Set(middleware.RequestIDHeader, tt.header)
			}
			// Track the header chi will see (after middleware normalization).
			// ADR-0050: it must ALWAYS be empty so chi generates its own.
			var seenHeader string
			handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenHeader = r.Header.Get(middleware.RequestIDHeader)
				gotID = middleware.GetReqID(r.Context())
				_, gotFlag = invalidRequestIDFrom(r.Context())
				gotClientID = clientRequestIDFrom(r.Context())
			}))
			handler.ServeHTTP(rec, req)

			if tt.wantGenerated && gotID == "" {
				t.Errorf("expected a non-empty chi-generated request id, got empty")
			}
			// The generated id must NEVER equal the client-supplied value —
			// that's the whole point of ADR-0050.
			if tt.header != "" && gotID == tt.wantClientID {
				t.Errorf("gateway id should differ from client value %q, got %q", tt.wantClientID, gotID)
			}
			if gotFlag != tt.wantFlag {
				t.Errorf("invalidRequestID flag = %v, want %v", gotFlag, tt.wantFlag)
			}
			// Header must ALWAYS be cleared before chi, regardless of input —
			// the gateway no longer forwards client values to chi.
			if seenHeader != "" {
				t.Errorf("header should always be cleared before chi, got %q", seenHeader)
			}
			if gotClientID != tt.wantClientID {
				t.Errorf("client_request_id on ctx = %q, want %q", gotClientID, tt.wantClientID)
			}
		})
	}
}
