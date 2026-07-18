package proxy

import (
	"context"
	"net/http"

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

// requestIDMiddleware replaces chi's middleware.RequestID with a variant that
// rejects nil/zero client request-ids (normalizeRequestID) and regenerates them
// gateway-side instead of propagating an unjoinable "0000...0000" value through
// the access log, request_logs, trace_payloads, and the OTel span.
//
// When the client header is valid it is used as-is (unchanged behavior); when it
// is absent or a nil-uuid, the header is cleared before delegating to chi's
// RequestID, which then mints its own "host/random-000001" id — exactly the path
// a standard OpenAI client (which never sends the header) already takes. So a
// rejected zero id is restored to the same handling as if it had never been
// sent, rather than corrupting correlation.
//
// A rejected id is logged (Warn, with the raw value + remote) here, and counted
// (with agent_type + tenant labels) later from the chat handler once those are
// known — see invalidRequestIDFrom consumers.
func requestIDMiddleware(next http.Handler) http.Handler {
	chiMW := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(middleware.RequestIDHeader)
		if raw != "" {
			if id, ok := normalizeRequestID(raw); ok {
				// Valid: normalize (trim) and pass through to chi unchanged.
				r.Header.Set(middleware.RequestIDHeader, id)
			} else {
				// Nil/zero id: clear the header so chi generates a fresh one,
				// and stash the rejected value for labeled counting downstream.
				r.Header.Del(middleware.RequestIDHeader)
				ctx := withInvalidRequestID(r.Context(), invalidRequestIDInfo{
					raw:    raw,
					remote: r.RemoteAddr,
				})
				r = r.WithContext(ctx)
				observability.Logger().Warn("invalid client request_id normalized; generating gateway-side id",
					"raw", raw,
					"remote", r.RemoteAddr,
				)
			}
		}
		chiMW.ServeHTTP(w, r)
	})
}
