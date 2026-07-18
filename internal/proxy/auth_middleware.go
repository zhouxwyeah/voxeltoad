package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"voxeltoad/internal/auth"
)

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

// authMiddleware authenticates the Bearer API key and injects the resolved
// KeyRecord into the request context. It rejects missing/malformed/invalid/
// expired keys with 401 in OpenAI-compatible error shape.
func authMiddleware(a *auth.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := bearerToken(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "authentication_error", "missing or malformed Authorization header")
				return
			}
			rec, err := a.Authenticate(r.Context(), key)
			if err != nil {
				msg := "invalid api key"
				if errors.Is(err, auth.ErrKeyExpired) {
					msg = "api key expired"
				}
				writeError(w, http.StatusUnauthorized, "authentication_error", msg)
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, rec)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. Returns ok=false if absent or not a non-empty Bearer token.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
