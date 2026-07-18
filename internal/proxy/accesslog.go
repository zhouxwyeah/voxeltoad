package proxy

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"voxeltoad/internal/observability"
)

// accessLogFormatter implements chi middleware.LogFormatter, emitting one
// structured log line per request via the process-wide slog logger. Format
// (text/json) is controlled by GATEWAY_LOG_FORMAT. When the request passes
// authentication, tenant, group and api_key_id are included.
type accessLogFormatter struct{}

func (f *accessLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &accessLogEntry{req: r, start: time.Now()}
}

type accessLogEntry struct {
	req   *http.Request
	start time.Time

	// Optional fields stashed by the handler so the single access-log line is
	// self-explanatory. errorType/provider/model come from the chat handler
	// (only meaningful for /v1/chat/completions); requestID is filled in Write
	// from chi's id so it covers every request including early rejections.
	errorType string
	provider  string
	model     string
}

func (e *accessLogEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ interface{}) {
	args := []any{
		"method", e.req.Method,
		"path", e.req.URL.Path,
		"status", status,
		"bytes", bytes,
		"duration_ms", elapsed.Milliseconds(),
		"remote", e.req.RemoteAddr,
		// Gateway-assigned per-request correlation id (or upstream trace header
		// when one was supplied). Lets every access line be grepped to its
		// error/audit/span (design/observability.md llm.request_id).
		"request_id", middleware.GetReqID(e.req.Context()),
	}

	// Include the authenticated identity when available (set by authMiddleware).
	if rec, ok := identityFrom(e.req.Context()); ok {
		args = append(args,
			"tenant", rec.Tenant,
			"group", rec.Group,
			"api_key_id", rec.KeyID,
		)
	}

	if e.model != "" {
		args = append(args, "model", e.model)
	}
	if e.provider != "" {
		args = append(args, "provider", e.provider)
	}
	if e.errorType != "" {
		args = append(args, "error_type", e.errorType)
	}

	observability.Logger().Info(e.req.Method+" "+e.req.URL.Path, args...)
}

func (e *accessLogEntry) Panic(v interface{}, stack []byte) {
	// Panics are already handled by middleware.Recoverer; log as context.
	observability.Logger().Error("access log panic",
		"panic", v,
		"path", e.req.URL.Path,
	)
}
