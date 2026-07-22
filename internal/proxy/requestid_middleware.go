package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"voxeltoad/internal/observability"
)

// invalidRequestIDCtxKey marks a request whose client-supplied request-id was a
// nil/zero value (rejected by normalizeRequestID). Carried on ctx so the handler
// can emit a labeled metric once the agent type and tenant are known (the
// middleware runs before auth, so it cannot label itself). The raw rejected
// value is stored alongside for the log line.
type invalidRequestIDCtxKey struct{}

type invalidRequestIDInfo struct {
	raw    string // the rejected client value (for logging)
	remote string // r.RemoteAddr at entry (for logging)
}

// withInvalidRequestID stashes the rejected-id info on ctx.
func withInvalidRequestID(ctx context.Context, info invalidRequestIDInfo) context.Context {
	return context.WithValue(ctx, invalidRequestIDCtxKey{}, info)
}

// invalidRequestIDFrom returns the rejected-id info on ctx, if any.
func invalidRequestIDFrom(ctx context.Context) (invalidRequestIDInfo, bool) {
	v, ok := ctx.Value(invalidRequestIDCtxKey{}).(invalidRequestIDInfo)
	return v, ok
}

// clientRequestIDCtxKey carries the client-supplied X-Request-Id header value
// verbatim (after trim). Empty when the client did not send the header. The
// gateway persists this separately as client_request_id (ADR-0050) — it is NOT
// used as the primary correlation key, because some clients reuse the same id
// across every request in a session.
type clientRequestIDCtxKey struct{}

func withClientRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clientRequestIDCtxKey{}, id)
}

// clientRequestIDFrom returns the original client X-Request-Id value from ctx,
// or "" when the client did not send one (or sent only whitespace).
func clientRequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(clientRequestIDCtxKey{}).(string)
	return v
}

// requestIDMiddleware replaces chi's middleware.RequestID with a variant that
// ALWAYS generates a gateway-side request id and never adopts the client value
// (ADR-0050). The client value is preserved on ctx via withClientRequestID so
// it can be recorded as client_request_id for cross-system correlation.
//
// Why always regenerate: some agent clients (Claude Code, Codex, …) reuse the
// same X-Request-Id across every request in a session. Adopting that value
// produced duplicate request_id rows in request_logs, breaking per-request
// correlation, UI lists, and any LIMIT 1 lookup. Regenerating gateway-side
// guarantees uniqueness; the original value survives in client_request_id.
//
// The nil-uuid detection (normalizeRequestID) stays as a labeled-warning
// trigger — those clients are almost certainly misconfigured, and operators
// want to see the metric. But it is now a strict subset of "always regenerate"
// rather than the only regeneration trigger.
func requestIDMiddleware(next http.Handler) http.Handler {
	chiMW := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(middleware.RequestIDHeader)
		if raw != "" {
			// Preserve the trimmed client value on ctx for separate
			// persistence as client_request_id (ADR-0050). We do NOT forward
			// it to chi — chi must always generate a fresh gateway id.
			clientID := strings.TrimSpace(raw)
			if clientID != "" {
				r = r.WithContext(withClientRequestID(r.Context(), clientID))
			}
			// Nil/zero id: flag for the labeled warning metric + log. The
			// header is cleared below regardless (always-regenerate path), so
			// the flag is purely informational.
			if _, ok := normalizeRequestID(raw); !ok {
				r = r.WithContext(withInvalidRequestID(r.Context(), invalidRequestIDInfo{
					raw:    raw,
					remote: r.RemoteAddr,
				}))
				observability.Logger().Warn("invalid client request_id; generating gateway-side id",
					"raw", raw,
					"remote", r.RemoteAddr,
				)
			}
		}
		// Always clear the header so chi's middleware.RequestID generates its
		// own "host/random-000001" id rather than forwarding the client value.
		r.Header.Del(middleware.RequestIDHeader)
		chiMW.ServeHTTP(w, r)
	})
}
