package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"voxeltoad/internal/auth"
	"voxeltoad/internal/ingress"
)

// codecForPath returns the ingress codec appropriate for the request path. It
// is used by middleware that runs before route dispatch (auth) so they can
// return protocol-shaped errors without yet knowing the matched handler's
// codec. /v1/messages → Anthropic codec; everything else → OpenAI codec (the
// default and historical shape).
func codecForPath(path string) ingress.Codec {
	if strings.HasPrefix(path, "/v1/messages") {
		return ingress.Lookup(ingress.ProtocolAnthropic)
	}
	return ingress.Lookup(ingress.ProtocolOpenAI)
}

// identityKey is the context key under which the authenticated KeyRecord is
// stored for downstream handlers.
type identityKeyType struct{}

var identityKey identityKeyType

// identityFrom returns the authenticated KeyRecord from the request context, if
// the auth middleware ran. Consumed by the handler to populate the plugin
// Context's tenant/group/key.
func identityFrom(ctx context.Context) (auth.KeyRecord, bool) {
	rec, ok := ctx.Value(identityKey).(auth.KeyRecord)
	return rec, ok
}

// modelAllowed reports whether the key may request the given model alias. An
// empty AllowedModels means unrestricted (all models). Otherwise the alias must
// be listed (ADR-0006 per-key model restriction).
func modelAllowed(rec auth.KeyRecord, model string) bool {
	if len(rec.AllowedModels) == 0 {
		return true
	}
	for _, m := range rec.AllowedModels {
		if m == model {
			return true
		}
	}
	return false
}

// authMiddleware authenticates the API key (Authorization: Bearer or x-api-key)
// and injects the resolved KeyRecord into the request context. The error
// envelope shape is protocol-specific (OpenAI vs Anthropic): it is selected by
// the request path so /v1/messages returns an Anthropic envelope and
// /v1/chat/completions returns the OpenAI envelope.
func authMiddleware(a *auth.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			codec := codecForPath(r.URL.Path)
			key, ok := bearerToken(r)
			if !ok {
				writeCodecErr(w, codec, http.StatusUnauthorized, "authentication_error", "missing or malformed Authorization header")
				return
			}
			rec, err := a.Authenticate(r.Context(), key)
			if err != nil {
				msg := "invalid api key"
				if errors.Is(err, auth.ErrKeyExpired) {
					msg = "api key expired"
				}
				writeCodecErr(w, codec, http.StatusUnauthorized, "authentication_error", msg)
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, rec)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from either:
//   - Authorization: Bearer <token>  (OpenAI convention; primary)
//   - x-api-key: <token>             (Anthropic convention; fallback)
//
// Returns ok=false if neither header carries a non-empty token. Both
// conventions are accepted on every /v1/ route so e.g. Claude Code (which
// defaults to x-api-key but can be configured with ANTHROPIC_AUTH_TOKEN to
// send Bearer) works without route-specific middleware.
func bearerToken(r *http.Request) (string, bool) {
	// Primary: Authorization: Bearer
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			if t := strings.TrimSpace(h[len(prefix):]); t != "" {
				return t, true
			}
		}
	}
	// Fallback: x-api-key (Anthropic clients including Claude Code).
	if k := strings.TrimSpace(r.Header.Get("x-api-key")); k != "" {
		return k, true
	}
	return "", false
}
