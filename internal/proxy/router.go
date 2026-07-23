// Package proxy is the data-plane core: it builds the HTTP router, runs the
// plugin chain, resolves routes, and forwards/streams requests to upstream
// providers. This file wires the router and the non-streaming chat handler.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/apperr"
	"voxeltoad/internal/auth"
	"voxeltoad/internal/config"
	"voxeltoad/internal/ingress"
	_ "voxeltoad/internal/ingress/anthropic" // register Anthropic ingress codec (/v1/messages)
	_ "voxeltoad/internal/ingress/openai"    // register OpenAI ingress codec (/v1/chat/completions)
	"voxeltoad/internal/observability"
	"voxeltoad/internal/plugin"
)

// Option configures the Router.
type Option func(*routerConfig)

// DispatcherProvider returns the current Dispatcher. It is resolved per request
// so the dispatcher can be hot-swapped (rebuilt on a config-snapshot change)
// without rebuilding the router. May return nil (chat → 501).
type DispatcherProvider func() *Dispatcher

type routerConfig struct {
	authn          *auth.Authenticator
	plugins        *plugin.Chain
	dispProv       DispatcherProvider
	sessionHeaders []string
	traceHeaders   []string
	auditRecorder  observability.RequestLogRecorder
	traceRecorder  observability.TracePayloadRecorder
	settingsSource func() *config.GatewaySettings
	ready          ReadinessProbe
	accessLog      bool
}

// defaultSessionHeader is the session-id header consulted for affinity routing
// when none is configured (ADR-0018).
const defaultSessionHeader = "X-Voxeltoad-Session"

// Response headers echoing the correlation ids so callers can join gateway
// logs/usage to their own traces. They mirror the incoming trace headers and the
// session header, in the response direction.
const (
	headerRequestID = "X-Request-Id"
	headerSessionID = "X-Voxeltoad-Session"
	headerTraceID   = "X-Trace-Id"
)

// echoCorrelationHeaders sets the correlation-id response headers. It is a
// no-op for empty values; safe to call on every exit path (success/error).
func echoCorrelationHeaders(w http.ResponseWriter, requestID, sessionID, traceID string) {
	if requestID != "" {
		w.Header().Set(headerRequestID, requestID)
	}
	if sessionID != "" {
		w.Header().Set(headerSessionID, sessionID)
	}
	if traceID != "" {
		w.Header().Set(headerTraceID, traceID)
	}
}

// WithSessionHeaders configures the candidate HTTP header names used to extract
// the session key for session_affinity routing (ADR-0018), in priority order.
// Empty preserves the default (X-Voxeltoad-Session). Different agent frameworks are
// supported by adding their header name here — no per-agent code.
func WithSessionHeaders(headers ...string) Option {
	return func(c *routerConfig) { c.sessionHeaders = headers }
}

// defaultTraceHeaders are the incoming request headers consulted, in priority
// order, for an upstream trace/request ID before falling back to the gateway-
// generated chi middleware ID.
var defaultTraceHeaders = []string{"X-Request-Id", "X-Trace-Id", "traceparent"}

// WithTraceHeaders overrides the default list of incoming request headers used
// to extract a trace/correlation id. Each different agent/caller can carry its
// own trace header — configure the names it sends here.
func WithTraceHeaders(headers ...string) Option {
	return func(c *routerConfig) { c.traceHeaders = headers }
}

// WithAuth enables API-key authentication on the /v1 routes using the given
// authenticator. Without it, the routes are open (used in tests and until auth
// is wired in deployment).
func WithAuth(a *auth.Authenticator) Option {
	return func(c *routerConfig) { c.authn = a }
}

// WithPlugins installs the governance plugin chain. The Pre phase runs before
// dispatch (a plugin that Stops the chain rejects the request with 429); the
// Post phase runs on response completion (the completion hook for billing/quota
// debit and audit; ADR-0012). Without it, no plugins run.
func WithPlugins(chain *plugin.Chain) Option {
	return func(c *routerConfig) { c.plugins = chain }
}

// WithDispatcherProvider supplies the dispatcher dynamically (resolved per
// request), enabling hot-swap on config change. It overrides the static disp
// passed to Router.
func WithDispatcherProvider(p DispatcherProvider) Option {
	return func(c *routerConfig) { c.dispProv = p }
}

// WithAuditRecorder installs the request-audit ledger recorder. When set, every
// request (success or rejection) appends one row to request_logs (fail-open,
// off the hot path). Without it, only telemetry (trace+metric) is emitted.
func WithAuditRecorder(rec observability.RequestLogRecorder) Option {
	return func(c *routerConfig) { c.auditRecorder = rec }
}

// WithTracePayloadRecorder installs the trace-payload recorder (ADR-0039). The
// recorder is always active (started in OpenStores); whether capture actually
// happens is gated per-request by the hot-reloadable GatewaySettings. Pass nil
// to disable trace recording entirely (e.g. in tests).
func WithTracePayloadRecorder(rec observability.TracePayloadRecorder) Option {
	return func(c *routerConfig) { c.traceRecorder = rec }
}

// WithSettingsSource installs the gateway-settings source the per-request
// telemetry accumulator reads to decide, hot-reloadable, whether to capture
// trace payloads and at what body cap (ADR-0039). Typically cfgStore.Settings.
// When nil, capture is off (settings source returns a zero-value GatewaySettings).
func WithSettingsSource(src func() *config.GatewaySettings) Option {
	return func(c *routerConfig) { c.settingsSource = src }
}

// ReadinessProbe reports whether the data plane is ready to serve traffic:
// dynamic config has been fetched at least once AND the quota store (PG) is
// reachable. Used by the /readyz endpoint for orchestration-system readiness
// probes (distinct from /healthz, which is a bare liveness check).
type ReadinessProbe interface {
	Ready(ctx context.Context) bool
}

// WithReadinessProbe installs the probe consulted by /readyz. Without it
// /readyz behaves like /healthz (always 200) — useful for tests that don't
// wire a real probe.
func WithReadinessProbe(p ReadinessProbe) Option {
	return func(c *routerConfig) { c.ready = p }
}

// WithAccessLog enables per-request HTTP access logging via the process-wide
// structured logger. Each request emits one line with method, path, status,
// bytes, duration, remote address, and (when authenticated) tenant/group/key.
// Format is controlled by GATEWAY_LOG_FORMAT (text/json).
func WithAccessLog() Option {
	return func(c *routerConfig) { c.accessLog = true }
}

// Router builds the data-plane HTTP handler exposing the OpenAI-compatible API.
// disp routes chat requests to upstreams (with failover); when nil (e.g. before
// routing is wired), chat returns 501 Not Implemented. For hot-swappable
// routing, pass nil and use WithDispatcherProvider.
func Router(disp *Dispatcher, opts ...Option) http.Handler {
	cfg := &routerConfig{}
	for _, o := range opts {
		o(cfg)
	}
	// Resolve the dispatcher per request: a provider (hot-swap) takes precedence
	// over the static disp.
	provider := cfg.dispProv
	if provider == nil {
		provider = func() *Dispatcher { return disp }
	}

	// Session-key extractor for affinity routing (ADR-0018).
	headers := cfg.sessionHeaders
	if len(headers) == 0 {
		headers = []string{defaultSessionHeader}
	}
	extractor := sessionKeyExtractor{headers: headers}

	traceHdrs := cfg.traceHeaders
	if len(traceHdrs) == 0 {
		traceHdrs = defaultTraceHeaders
	}

	// Trace-payload recorder defaults to the noop recorder when unset (tests).
	// settingsSource is read per-request by each accumulator, so whether capture
	// happens (and the body cap) are hot-reloadable from the admin UI (ADR-0039).
	tracePL := cfg.traceRecorder
	if tracePL == nil {
		tracePL = observability.NoopTracePayloadRecorder
	}
	settingsSource := cfg.settingsSource

	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(middleware.Recoverer)
	if cfg.accessLog {
		r.Use(middleware.RequestLogger(&accessLogFormatter{}))
	}

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// /readyz is the readiness probe (distinct from the liveness /healthz).
	// 503 until dynamic config has been fetched AND the quota store is reachable;
	// 200 once both are healthy. Unauthenticated (probe-only, like /healthz).
	r.Get("/readyz", readyzHandler(cfg.ready))

	r.Route("/v1", func(r chi.Router) {
		if cfg.authn != nil {
			r.Use(authMiddleware(cfg.authn))
		}
		r.Post("/chat/completions", chatCompletionsHandler(provider, cfg.plugins, extractor, traceHdrs, cfg.auditRecorder, tracePL, settingsSource))
		r.Post("/messages", messagesHandler(provider, cfg.plugins, extractor, traceHdrs, cfg.auditRecorder, tracePL, settingsSource))
		r.Get("/models", notImplemented)
	})

	return r
}

// newPluginContext builds the per-request plugin Context from the request and
// the authenticated identity (if any).
func newPluginContext(r *http.Request, req *adapter.UnifiedRequest) *plugin.Context {
	c := &plugin.Context{Ctx: r.Context(), Request: req}
	if rec, ok := identityFrom(r.Context()); ok {
		c.Tenant = rec.Tenant
		c.Group = rec.Group
		c.APIKeyID = rec.KeyID
	}
	return c
}

func chatCompletionsHandler(provider DispatcherProvider, chain *plugin.Chain, extractor sessionKeyExtractor, traceHdrs []string, audit observability.RequestLogRecorder, tracePL observability.TracePayloadRecorder, settings func() *config.GatewaySettings) http.HandlerFunc {
	return serveChat(ingress.Lookup(ingress.ProtocolOpenAI), provider, chain, extractor, traceHdrs, audit, tracePL, settings)
}

func messagesHandler(provider DispatcherProvider, chain *plugin.Chain, extractor sessionKeyExtractor, traceHdrs []string, audit observability.RequestLogRecorder, tracePL observability.TracePayloadRecorder, settings func() *config.GatewaySettings) http.HandlerFunc {
	codec := ingress.Lookup(ingress.ProtocolAnthropic)
	inner := serveChat(codec, provider, chain, extractor, traceHdrs, audit, tracePL, settings)
	return func(w http.ResponseWriter, r *http.Request) {
		// Hot-reloadable Anthropic ingress switch (ADR-0048). When disabled
		// the gateway answers 404 in the Anthropic envelope so clients treat
		// it as "endpoint absent" (terminal), not 503 "retry later". The zero
		// value (AnthropicDisabled=false) = enabled — the default. A nil
		// settings source (tests with Router(nil)) is treated as "all enabled".
		if settings != nil {
			if s := settings(); s != nil && s.Ingress.AnthropicDisabled {
				writeCodecErr(w, codec, http.StatusNotFound, "not_found_error", "anthropic ingress disabled")
				return
			}
		}
		inner.ServeHTTP(w, r)
	}
}

// serveChat is the protocol-agnostic chat handler shared by /v1/chat/completions
// and /v1/messages. The ingress codec owns the inbound wire translation
// (request decoding, response encoding, error envelope, SSE frame shape);
// everything else — auth, agent detection, session routing, plugins,
// dispatcher, telemetry/audit/billing — is identical across protocols.
//
// Per-request agent detection and affinity extraction remain in the handler
// body because they inspect the raw request (headers, body) before the codec
// decodes it. Everything from body decode onwards is delegated here so a new
// ingress protocol wires up by registering a codec and pointing its route at
// serveChat with that codec.
func serveChat(codec ingress.Codec, provider DispatcherProvider, chain *plugin.Chain, extractor sessionKeyExtractor, traceHdrs []string, audit observability.RequestLogRecorder, tracePL observability.TracePayloadRecorder, settings func() *config.GatewaySettings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Detect the calling agent (claude-code, codex, …) from the User-Agent /
		// x-<vendor>-session-id headers once, so every exit path's telemetry
		// carries the same label. "" when unrecognized (a plain OpenAI client).
		agentType := detectAgent(r)

		// Read the raw body once; we need it for two-step decoding because
		// UnifiedRequest implements UnmarshalJSON (which captures unknown
		// fields into Extra). Embedding UnifiedRequest in an outer struct
		// would promote its UnmarshalJSON and shadow the outer fields
		// (user, prompt_cache_key), so we decode them separately.
		rawBody, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			rid, sid, tid := requestAndSessionIDs(r, traceHdrs, extractor, nil)
			acc := newTelemetryAcc("", false, rid, sid, tid, settings)
			acc.agentType = agentType
			defer func() { acc.emit(r.Context(), nil, audit, tracePL) }()
			acc.errType = apperr.InvalidRequestBody.Code
			writeAppErrCodec(w, codec, apperr.InvalidRequestBody, err.Error())
			return
		}

		// The ingress codec owns the inbound wire shape: it translates the
		// client's protocol (OpenAI for /v1/chat/completions, Anthropic for
		// /v1/messages) into the unified request model. Everything downstream
		// (normalize / plugins / dispatcher / adapter) is protocol-agnostic
		// and sees the same unified shape regardless of how the request came in.
		req, err := codec.DecodeRequest(rawBody)
		if err != nil {
			rid, sid, tid := requestAndSessionIDs(r, traceHdrs, extractor, nil)
			acc := newTelemetryAcc("", false, rid, sid, tid, settings)
			acc.agentType = agentType
			defer func() { acc.emit(r.Context(), nil, audit, tracePL) }()
			acc.errType = apperr.InvalidRequestBody.Code
			writeAppErrCodec(w, codec, apperr.InvalidRequestBody, err.Error())
			return
		}
		// Decode affinity fields separately to avoid the promoted
		// UnmarshalJSON issue. session_id (top-level) and metadata.session_id
		// are read for multi-source session-key resolution (Cline/OpenRouter
		// use the former; LiteLLM proxy the latter).
		var affinity struct {
			User      string `json:"user"`
			SessionID string `json:"session_id"`
			Metadata  struct {
				SessionID string `json:"session_id"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(rawBody, &affinity) // best-effort; errors ignored

		// Extract the session key (ADR-0018) and carry it on the context so the
		// dispatcher's session_affinity routing can stick this session to one
		// provider. No-op for other strategies. The source label is carried
		// alongside for observability.
		affinityID := bodyIdentity{
			SessionID:         affinity.SessionID,
			MetadataSessionID: affinity.Metadata.SessionID,
			User:              affinity.User,
		}
		sessKey, sessSource := extractor.key(r, req, affinityID)
		r = r.WithContext(withSessionKey(r.Context(), sessKey))
		r = r.WithContext(withSessionSource(r.Context(), sessSource))

		// Count malformed client-supplied session ids (DEFECT-A) so operators
		// can see which sources carry dirty values. Labeled by source + tenant
		// (both low-cardinality). Tenant comes from the authenticated key.
		if rec, ok := identityFrom(r.Context()); ok && rec.Tenant != "" {
			for _, src := range extractor.invalidSessionSources(r, affinityID) {
				observability.RecordSessionIDInvalid(r.Context(), src, rec.Tenant)
			}
		}

		// Resolve request-id (chi middleware), session-id (X-Voxeltoad-Session
		// header), and the W3C trace-id (parsed from traceparent) for tracing and
		// the audit ledger.
		rid, sid, tid := requestAndSessionIDs(r, traceHdrs, extractor, req)
		sessSrc := sessionSourceFrom(r.Context())

		// Echo correlation ids on every response (success, error, or stream) so
		// callers can join gateway logs/usage to their own traces. Set once here;
		// the same ResponseWriter flows into the stream handler.
		echoCorrelationHeaders(w, rid, sid, tid)

		// Single collection point: the accumulator is filled through the branches
		// below and emitted once here on EVERY exit path (success or rejection),
		// fanning out to telemetry (trace+metric) and the audit ledger.
		acc := newTelemetryAcc("", false, rid, sid, tid, settings)
		acc.sessionSource = sessSrc
		acc.agentType = agentType
		acc.ingressProtocol = string(codec.Protocol()) // llm.ingress.protocol (ADR-0045)
		pc := newPluginContext(r, req)
		pc.RequestID = rid
		pc.SessionID = sid
		pc.TraceID = tid
		pc.SessionSource = sessSrc
		defer func() { acc.emit(r.Context(), pc, audit, tracePL) }()

		acc.modelRequested = req.Model
		acc.stream = req.Stream

		// Capture the request-side trace payload (raw client body + normalized
		// message array) once the body is parsed. Best-effort and a no-op when
		// capture is disabled (ADR-0039).
		acc.captureRequest(rawBody, req)

		// Mirror the outcome onto the access-log entry (when the access logger is
		// installed) so the single access line is self-explanatory: agent, model,
		// provider, token usage, TTFT and error_type accompany status — the same
		// fields the audit ledger persists, so the desktop log viewer alone is
		// enough to see who called what and what it cost. request_id is added by
		// the entry itself (covers every request), so it is not set here.
		if ale, ok := middleware.GetLogEntry(r).(*accessLogEntry); ok {
			modelReq := req.Model
			defer func() {
				ale.model = modelReq
				ale.provider = acc.provider
				ale.errorType = acc.errType
				ale.agentType = acc.agentType
				ale.sessionID = acc.sessionID
				ale.modelResolved = acc.modelResolved
				ale.stream = acc.stream
				if acc.usage != nil {
					ale.promptTokens = acc.usage.PromptTokens
					ale.completeTokens = acc.usage.CompletionTokens
					ale.totalTokens = acc.usage.TotalTokens
					ale.cachedTokens = acc.usage.CachedPromptTokens
				}
				ale.ttftMs = acc.ttft.Milliseconds()
				ale.retries = acc.retryCount
				ale.fallback = acc.fallback
			}()
		}

		// Authorization: a key restricted via AllowedModels may only request those
		// aliases (empty = all). Checked before dispatch/plugins so a disallowed
		// model never reaches an upstream or gets billed (ADR-0006). Independent
		// of backend availability, so it precedes the dispatcher check.
		if rec, ok := identityFrom(r.Context()); ok && !modelAllowed(rec, req.Model) {
			acc.errType = apperr.ModelNotPermitted.Code
			writeAppErrCodec(w, codec, apperr.ModelNotPermitted, "model "+req.Model)
			return
		}

		disp := provider()
		if disp == nil {
			acc.errType = "api_error"
			http.Error(w, "not implemented", http.StatusNotImplemented)
			return
		}

		alias := req.Model // the client-facing model name is the routing alias
		// Carry the ingress protocol on the context so the dispatcher can
		// prefer providers whose adapter speaks the same wire protocol
		// (protocol-aware routing, ADR-0047) — passthrough becomes a natural
		// consequence of routing, not a router-layer special case.
		r = r.WithContext(withIngressProtocol(r.Context(), string(codec.Protocol())))
		pc.Ctx = r.Context()

		// Pre phase: rate limit / quota / sensitive-word checks may reject.
		if chain != nil {
			if err := chain.Run(pc, plugin.PhasePre); err != nil {
				acc.errType = apperr.Unexpected.Code
				writeAppErrCodec(w, codec, apperr.Unexpected, err.Error())
				return
			}
			if pc.Stop {
				status, typ := rejectStatus(pc)
				acc.errType = typ
				writeCodecErr(w, codec, status, typ, "request blocked by "+pc.BlockedBy)
				return
			}
		}

		if req.Stream {
			streamChatCompletions(w, r, disp, alias, req, chain, pc, acc, codec)
			return
		}

		resp, dr, err := disp.Forward(r.Context(), alias, req)
		if err != nil {
			// Run Post even on failure so the Pre reservation (e.g. billing's
			// quota pre-debit) is reconciled/refunded — no usage ⇒ full refund
			// (ADR-0013/0016). dr.Provider is the last attempted upstream.
			runPost(chain, pc, &adapter.UnifiedResponse{Model: req.Model}, dr)
			status, typ := mapForwardError(err)
			acc.errType = typ
			acc.errMsg = truncate([]byte(err.Error()), 256)
			acc.setResult(dr, nil)
			acc.captureError(status, upstreamErrorBody(err))
			logForwardFailure(r, rid, sid, req.Model, dr.Provider, typ, err)
			writeCodecErr(w, codec, status, typ, err.Error())
			return
		}

		// Completion hook: Post phase (billing/quota debit, audit) — ADR-0012.
		runPost(chain, pc, resp, dr)
		acc.setResult(dr, resp.Usage)
		acc.captureResponse(resp, http.StatusOK)

		// Prefer the original upstream response body when available (codec
		// EncodeResponse returns resp.Raw verbatim), avoiding data loss from a
		// re-encode round-trip (system_fingerprint, logprobs, extra choice
		// fields, etc.). Falls back to re-marshalling when Raw is nil.
		body, err := codec.EncodeResponse(resp)
		if err != nil {
			acc.errType = "api_error"
			writeCodecErr(w, codec, http.StatusInternalServerError, "api_error", "failed to encode response")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// runPost populates the plugin Context with the completed response, hit
// provider + endpoint and runs the Post phase (the completion hook). Safe with
// a nil chain.
func runPost(chain *plugin.Chain, pc *plugin.Context, resp *adapter.UnifiedResponse, dr DispatchResult) {
	if chain == nil {
		return
	}
	pc.Response = resp
	pc.Provider = dr.Provider
	pc.ProviderEndpoint = dr.Endpoint
	_ = chain.Run(pc, plugin.PhasePost)
}

// rejectStatus maps a Pre-phase rejection to an HTTP status and an
// OpenAI-compatible error type. A plugin may set Context.RejectStatus (e.g. 402
// quota exhausted, 503 quota store unreachable); the default is 429 (rate
// limit) — ADR-0013.
func rejectStatus(pc *plugin.Context) (status int, errType string) {
	switch pc.RejectStatus {
	case http.StatusPaymentRequired:
		return http.StatusPaymentRequired, "insufficient_quota"
	case http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable, "api_error"
	default:
		return http.StatusTooManyRequests, "rate_limit_error"
	}
}

// mapForwardError maps a forwarding failure to an HTTP status and an
// OpenAI-compatible error type.
func mapForwardError(err error) (status int, errType string) {
	var ue *upstreamError
	if errors.As(err, &ue) {
		switch ue.kind {
		case errTimeout:
			return http.StatusGatewayTimeout, "timeout_error"
		case errBuild:
			return http.StatusInternalServerError, "api_error"
		default: // errUpstream4xx / errUpstream5xx — the gateway failed to get a
			// good upstream answer; surface as 502 regardless of upstream status.
			return http.StatusBadGateway, "upstream_error"
		}
	}
	return http.StatusBadGateway, "upstream_error"
}

// writeError emits an OpenAI-compatible error envelope: {"error":{...}}.
// It delegates to the OpenAI ingress codec so the wire shape lives in one
// place. For protocol-aware error responses (e.g. Anthropic inbound), use
// writeCodecErr with the request's ingress codec.
func writeError(w http.ResponseWriter, status int, errType, message string) {
	writeCodecErr(w, ingress.Lookup(ingress.ProtocolOpenAI), status, errType, message)
}

// writeCodecErr emits an error body in the codec's wire format and writes the
// HTTP status. Used by handlers and middleware that know the inbound protocol
// (e.g. /v1/messages → anthropic codec).
func writeCodecErr(w http.ResponseWriter, codec ingress.Codec, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(codec.EncodeError(status, errType, message))
}

// logForwardFailure emits a server-side error-level log line for an upstream
// forwarding failure (the 502/504/500 surfaced to the client). The underlying
// cause is otherwise only sent to the client in the response body and lost from
// server logs, making 5xx impossible to diagnose from logs alone. The message
// is truncated (see truncate) so a large/malicious upstream body can't blow up
// the log line; no prompt/completion bodies or credentials are included
// (design/observability.md §日志约定).
func logForwardFailure(r *http.Request, rid, sid, model, provider, errType string, err error) {
	if err == nil {
		return
	}
	observability.Logger().Error("upstream request failed",
		"request_id", rid,
		"session_id", sid,
		"model", model,
		"provider", provider,
		"error_type", errType,
		"error", truncate([]byte(err.Error()), 256),
	)
}

// writeAppErr emits the same envelope as writeError, driven by an apperr.Error.
// The message is the i18n key (the client resolves it); the type is the stable
// code. Use this in favor of inline writeError(...) so each domain lives in its
// own apperr file. Uses the OpenAI envelope by default.
func writeAppErr(w http.ResponseWriter, e *apperr.Error) {
	writeAppErrCodec(w, ingress.Lookup(ingress.ProtocolOpenAI), e, "")
}

// writeAppErrMsg is writeAppErr when the handler needs to append runtime context
// to the message (e.g. the underlying cause).
func writeAppErrMsg(w http.ResponseWriter, e *apperr.Error, ctx string) {
	writeAppErrCodec(w, ingress.Lookup(ingress.ProtocolOpenAI), e, ctx)
}

// writeAppErrCodec is the protocol-aware variant: it uses codec's envelope
// shape (Anthropic on /v1/messages, OpenAI elsewhere). Used by handlers that
// carry an ingress codec (chatCompletionsHandler / messagesHandler).
func writeAppErrCodec(w http.ResponseWriter, codec ingress.Codec, e *apperr.Error, ctx string) {
	msg := e.I18n
	if ctx != "" {
		msg = msg + ": " + ctx
	}
	writeCodecErr(w, codec, e.Status, e.Code, msg)
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// readyzHandler serves GET /readyz. When no ReadinessProbe is configured it
// always returns 200 (parity with /healthz). When a probe is installed it
// reports 200 only when the probe is ready (config fetched + DB reachable),
// otherwise 503 — so orchestration systems don't route traffic to an instance
// that would only return 501 from /v1/chat/completions.
func readyzHandler(p ReadinessProbe) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p == nil || p.Ready(r.Context()) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	}
}

// requestAndSessionIDs returns the gateway-assigned per-request correlation id
// (from an incoming trace header, or chi middleware), the client-supplied
// session key (resolved by the session-key extractor and carried on ctx), and
// the W3C trace id parsed from the traceparent header (empty if absent or
// malformed). traceHdrs is the ordered list of headers to check for an upstream
// trace/correlation id; if none match, chi's middleware.RequestID is used as
// fallback. The session id is read from ctx (where the handler's earlier
// extractor.key call stored it with full body context); the req/ex args are kept
// for the body-read-failure paths that have no ctx value yet.
func requestAndSessionIDs(r *http.Request, traceHdrs []string, ex sessionKeyExtractor, req *adapter.UnifiedRequest) (requestID, sessionID, traceID string) {
	// Check incoming trace headers first, fall back to chi.
	requestID = middleware.GetReqID(r.Context())
	for _, h := range traceHdrs {
		if v := r.Header.Get(h); v != "" {
			if h == "traceparent" {
				// A W3C traceparent is never a request id. Parse its trace
				// segment into traceID if well-formed, and continue looking
				// for a real correlation id.
				if tid, ok := parseTraceparent(v); ok {
					traceID = tid
				}
				continue
			}
			// Reject nil/zero ids (e.g. "0000...0000") so a dirty client header
			// can't overwrite the good id chi already minted (Part 1). Keep
			// scanning the remaining headers instead of adopting the bad value.
			if id, ok := normalizeRequestID(v); ok {
				requestID = id
				break
			}
		}
	}
	// Prefer the session key already resolved by the handler (carries full
	// body context: session_id, metadata.session_id). Fall back to a header-only
	// extraction on the early-failure paths where ctx has none.
	if sk := sessionKeyFrom(r.Context()); sk != "" {
		sessionID = sk
	} else if req != nil {
		sessionID, _ = ex.key(r, req, bodyIdentity{})
	}
	return
}

// parseTraceparent extracts the 32-hex trace id from a W3C traceparent header
// of the form "version-traceid-spanid-traceflags" (e.g.
// "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"). It returns
// ("", false) for anything not matching that exact shape so a malformed value
// degrades gracefully to "no trace id" rather than corrupting the field.
func parseTraceparent(h string) (string, bool) {
	const sep = "-"
	parts := strings.Split(h, sep)
	if len(parts) != 4 {
		return "", false
	}
	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]
	// version and flags are 2 hex chars; traceid is 32; spanid is 16.
	if len(version) != 2 || len(traceID) != 32 || len(spanID) != 16 || len(flags) != 2 {
		return "", false
	}
	if !isHex(version) || !isHex(traceID) || !isHex(spanID) || !isHex(flags) {
		return "", false
	}
	return traceID, true
}

// isHex reports whether s consists solely of hexadecimal digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
