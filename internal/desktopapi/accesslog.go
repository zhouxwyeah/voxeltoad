// Access logging for the desktop read API (/api/v1/*).
//
// The data plane (/v1/*) already logs one line per request via chi's
// middleware (proxy.WithAccessLog); the read API had none, so the desktop
// Logs page showed only startup lines even while the UI was actively calling
// /api/v1/* endpoints. This middleware closes that gap.
package desktopapi

import (
	"net/http"
	"time"

	"voxeltoad/internal/observability"
)

// accessLogSkip lists read-API paths that must NOT produce log lines: the log
// viewer polls /api/v1/logs every few seconds (logging it would flood the
// 2000-line ring with self-referential noise and push out useful entries),
// and scripts probe /health for readiness.
var accessLogSkip = map[string]bool{
	"/api/v1/logs":   true,
	"/api/v1/health": true,
}

// statusRecorder captures the response status for the log line (handlers that
// never call WriteHeader, e.g. writeJSON, implicitly send 200).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// WithAccessLog wraps the read API with one structured log line per request —
// method, path, query (when present), status, duration — via the process slog
// logger, so lines land in the same ring/file/stderr fan-out as the data-plane
// access logs. Severity follows status: >=500 Error, >=400 Warn, else Info.
func WithAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accessLogSkip[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)

		args := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if q := r.URL.RawQuery; q != "" {
			args = append(args, "query", q)
		}
		logger := observability.Logger()
		msg := r.Method + " " + r.URL.Path
		switch {
		case rec.status >= 500:
			logger.Error(msg, args...)
		case rec.status >= 400:
			logger.Warn(msg, args...)
		default:
			logger.Info(msg, args...)
		}
	})
}
