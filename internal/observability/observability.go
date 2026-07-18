// Package observability wires OpenTelemetry tracing/metrics and structured
// logging for the gateway. It also defines the canonical LLM semantic
// attribute schema — the single source of truth for per-request fields.
//
// See design/observability.md: nothing should emit LLM telemetry by bypassing
// this schema.
package observability

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// LLM semantic attribute keys. These mirror the table in design/observability.md
// and MUST be used instead of ad-hoc attribute names.
const (
	AttrTenant             = "llm.tenant"
	AttrGroup              = "llm.group"
	AttrAPIKeyID           = "llm.api_key_id"
	AttrModelRequested     = "llm.model.requested"
	AttrModelResolved      = "llm.model.resolved"
	AttrProvider           = "llm.provider"
	AttrStream             = "llm.stream"
	AttrTokensPrompt       = "llm.tokens.prompt"
	AttrTokensCompletion   = "llm.tokens.completion"
	AttrTokensTotal        = "llm.tokens.total"
	AttrTTFTms             = "llm.ttft_ms"
	AttrDurationms         = "llm.duration_ms"
	AttrCacheHit           = "llm.cache.hit"
	AttrCacheTier          = "llm.cache.tier"   // "upstream" (v1); future: "gateway"
	AttrCacheSource        = "llm.cache.source" // provider name supplying the cache
	AttrTokensCachedPrompt = "llm.tokens.cached_prompt"
	AttrPluginBlockedBy    = "llm.plugin.blocked_by"
	AttrRetryCount         = "llm.retry.count"
	AttrFallback           = "llm.fallback"
	AttrErrorType          = "llm.error.type"
	AttrErrorDetail        = "llm.error.detail"
	AttrRequestID          = "llm.request_id"
	AttrUpstreamRequestID  = "llm.upstream_request_id" // provider-assigned request id (OpenAI x-request-id, Anthropic request-id, …) for support/reconciliation
	AttrSessionID          = "llm.session_id"
	AttrTraceID            = "llm.trace_id"
	AttrSessionSource      = "llm.session_source"
	AttrAgentType          = "llm.agent_type"
)

// Provider configures observability initialization.
type Provider struct {
	ServiceName string
	Endpoint    string
	Enabled     bool
}

// Logger returns the process-wide structured logger. Format and level are
// controlled by GATEWAY_LOG_FORMAT (text/json) and GATEWAY_LOG_LEVEL
// (debug/info/warn/error); defaults are json + info. Never logs
// prompt/completion bodies (see design/observability.md).
func Logger() *slog.Logger {
	cfg := LogConfigFromEnv()
	opts := &slog.HandlerOptions{Level: cfg.Level}
	var h slog.Handler
	if cfg.Format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

// Init sets up the global tracer and meter providers. LLM telemetry (trace
// spans + metrics) is only EXPORTED, never stored by the gateway: when
// p.Endpoint is set, OTLP/HTTP exporters ship spans and metrics to a collector
// (Tempo/Prometheus/Grafana etc.); the data plane keeps nothing locally. When
// p.Endpoint is empty (or p.Enabled is false) providers are installed WITHOUT
// an exporter, so the rest of the code can emit unconditionally at ~no cost.
// Returns a shutdown function that flushes and stops both providers.
func Init(ctx context.Context, p Provider) (shutdown func(context.Context) error, err error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(p.ServiceName)),
	)
	if err != nil {
		return nil, err
	}

	var traceOpts []sdktrace.TracerProviderOption
	traceOpts = append(traceOpts, sdktrace.WithResource(res))
	var meterOpts []metric.Option
	meterOpts = append(meterOpts, metric.WithResource(res))

	if p.Enabled && p.Endpoint != "" {
		spanExp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(p.Endpoint), otlptracehttp.WithInsecure())
		if err != nil {
			return nil, err
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(spanExp))

		metricExp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpoint(p.Endpoint), otlpmetrichttp.WithInsecure())
		if err != nil {
			return nil, err
		}
		meterOpts = append(meterOpts, metric.WithReader(metric.NewPeriodicReader(metricExp)))
	}

	tp := sdktrace.NewTracerProvider(traceOpts...)
	otel.SetTracerProvider(tp)
	mp := metric.NewMeterProvider(meterOpts...)
	otel.SetMeterProvider(mp)
	// Rebuild the LLM metric instruments against the newly installed provider.
	initInstruments()

	return func(ctx context.Context) error {
		// Best-effort flush/stop both providers; return the first error.
		tErr := tp.Shutdown(ctx)
		mErr := mp.Shutdown(ctx)
		if tErr != nil {
			return tErr
		}
		return mErr
	}, nil
}
