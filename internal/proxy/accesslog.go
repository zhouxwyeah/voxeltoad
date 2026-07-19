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
	// self-explanatory. They mirror a subset of telemetryAcc (see router.go's
	// deferred copy): identity/routing (provider, model, modelResolved,
	// agentType, sessionID), usage (token counts, ttft), reliability (retries,
	// fallback, errorType). Only meaningful for /v1/chat/completions; requestID
	// is filled in Write from chi's id so it covers every request including
	// early rejections. Zero values are omitted from the line.
	errorType      string
	provider       string
	model          string
	modelResolved  string
	agentType      string
	sessionID      string
	stream         bool
	promptTokens   int
	completeTokens int
	totalTokens    int
	cachedTokens   int
	ttftMs         int64
	retries        int
	fallback       bool
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

	if e.agentType != "" {
		args = append(args, "agent_type", e.agentType)
	}
	if e.model != "" {
		args = append(args, "model", e.model)
	}
	if e.modelResolved != "" && e.modelResolved != e.model {
		args = append(args, "model_resolved", e.modelResolved)
	}
	if e.provider != "" {
		args = append(args, "provider", e.provider)
	}
	if e.stream {
		args = append(args, "stream", true)
	}
	if e.sessionID != "" {
		args = append(args, "session_id", e.sessionID)
	}
	if e.promptTokens != 0 {
		args = append(args, "prompt_tokens", e.promptTokens)
	}
	if e.completeTokens != 0 {
		args = append(args, "completion_tokens", e.completeTokens)
	}
	if e.totalTokens != 0 {
		args = append(args, "total_tokens", e.totalTokens)
	}
	if e.cachedTokens != 0 {
		args = append(args, "cached_tokens", e.cachedTokens)
	}
	if e.ttftMs != 0 {
		args = append(args, "ttft_ms", e.ttftMs)
	}
	if e.retries != 0 {
		args = append(args, "retries", e.retries)
	}
	if e.fallback {
		args = append(args, "fallback", true)
	}
	if e.errorType != "" {
		args = append(args, "error_type", e.errorType)
	}

	// Severity follows the outcome: upstream/forward failures (5xx) and client
	// rejections (4xx) must stand out in the desktop log viewer instead of
	// drowning in a uniform INFO stream.
	logger := observability.Logger()
	msg := e.req.Method + " " + e.req.URL.Path
	switch {
	case status >= 500:
		logger.Error(msg, args...)
	case status >= 400:
		logger.Warn(msg, args...)
	default:
		logger.Info(msg, args...)
	}
}

func (e *accessLogEntry) Panic(v interface{}, stack []byte) {
	// Panics are already handled by middleware.Recoverer; log as context.
	observability.Logger().Error("access log panic",
		"panic", v,
		"path", e.req.URL.Path,
	)
}
