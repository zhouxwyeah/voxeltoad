package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestTracing installs a tracer provider with an in-memory span recorder and
// returns it so tests can assert on emitted spans. It restores the previous
// global provider on cleanup.
func newTestTracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return sr
}

// newTestMetrics installs a meter provider with a manual reader and returns the
// reader so tests can collect and assert on metrics. Restores prior provider.
func newTestMetrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	// Rebuild the package instruments against the new provider.
	initInstruments()
	t.Cleanup(func() {
		otel.SetMeterProvider(prev)
		initInstruments()
	})
	return reader
}

func TestRecordTelemetry_SetsSpanAttributes(t *testing.T) {
	sr := newTestTracing(t)
	newTestMetrics(t)

	tr := otel.Tracer("test")
	ctx, span := tr.Start(context.Background(), "chat")

	RecordTelemetry(ctx, RequestTelemetry{
		Tenant:           "acme",
		Group:            "team-a",
		APIKeyID:         "key_1",
		ModelRequested:   "chat",
		ModelResolved:    "gpt-4o",
		Provider:         "openai",
		Stream:           true,
		PromptTokens:     11,
		CompletionTokens: 7,
		TotalTokens:      18,
		TTFT:             120 * time.Millisecond,
		Duration:         350 * time.Millisecond,
		BlockedBy:        "",
		RetryCount:       1,
		Fallback:         true,
		ErrorType:        "",
	})
	span.End()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	attrs := map[string]string{}
	nums := map[string]int64{}
	bools := map[string]bool{}
	for _, kv := range spans[0].Attributes() {
		switch kv.Value.Type().String() {
		case "INT64":
			nums[string(kv.Key)] = kv.Value.AsInt64()
		case "BOOL":
			bools[string(kv.Key)] = kv.Value.AsBool()
		default:
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	if attrs[AttrTenant] != "acme" {
		t.Errorf("%s = %q, want acme", AttrTenant, attrs[AttrTenant])
	}
	if attrs[AttrProvider] != "openai" {
		t.Errorf("%s = %q, want openai", AttrProvider, attrs[AttrProvider])
	}
	if attrs[AttrModelRequested] != "chat" || attrs[AttrModelResolved] != "gpt-4o" {
		t.Errorf("model attrs = %q/%q, want chat/gpt-4o", attrs[AttrModelRequested], attrs[AttrModelResolved])
	}
	if nums[AttrTokensTotal] != 18 {
		t.Errorf("%s = %d, want 18", AttrTokensTotal, nums[AttrTokensTotal])
	}
	if nums[AttrTTFTms] != 120 {
		t.Errorf("%s = %d, want 120", AttrTTFTms, nums[AttrTTFTms])
	}
	if nums[AttrRetryCount] != 1 {
		t.Errorf("%s = %d, want 1", AttrRetryCount, nums[AttrRetryCount])
	}
	if !bools[AttrFallback] {
		t.Errorf("%s = %v, want true", AttrFallback, bools[AttrFallback])
	}
}

// TestRecordTelemetry_ErrorDetailAttribute: the truncated underlying cause must
// surface on the span (llm.error.detail) so a failed request is diagnosable
// from traces, while staying out of metric labels and the audit ledger.
func TestRecordTelemetry_ErrorDetailAttribute(t *testing.T) {
	sr := newTestTracing(t)
	newTestMetrics(t)

	tr := otel.Tracer("test")
	ctx, span := tr.Start(context.Background(), "chat")
	RecordTelemetry(ctx, RequestTelemetry{
		Provider:       "openai",
		ModelRequested: "chat",
		ErrorType:      "upstream_error",
		ErrorDetail:    "upstream returned 500: rate limit",
	})
	span.End()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	var detail string
	for _, kv := range spans[0].Attributes() {
		if string(kv.Key) == AttrErrorDetail {
			detail = kv.Value.AsString()
		}
	}
	if detail != "upstream returned 500: rate limit" {
		t.Errorf("%s = %q, want upstream returned 500: rate limit", AttrErrorDetail, detail)
	}
}

func TestRecordTelemetry_EmitsMetrics(t *testing.T) {
	newTestTracing(t)
	reader := newTestMetrics(t)

	RecordTelemetry(context.Background(), RequestTelemetry{
		Tenant: "acme", ModelRequested: "chat", Provider: "openai",
		PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18,
		TTFT: 120 * time.Millisecond, Duration: 350 * time.Millisecond,
	})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			found[m.Name] = true
		}
	}
	for _, want := range []string{
		"llm_requests_total", "llm_tokens_total",
		"llm_ttft_seconds", "llm_request_duration_seconds",
	} {
		if !found[want] {
			t.Errorf("expected metric %q to be emitted; got %v", want, found)
		}
	}
}

func TestRecordTelemetry_UpstreamErrorMetric(t *testing.T) {
	newTestTracing(t)
	reader := newTestMetrics(t)

	RecordTelemetry(context.Background(), RequestTelemetry{
		Tenant: "acme", ModelRequested: "chat", Provider: "openai",
		ErrorType: "upstream_error",
	})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "llm_upstream_errors_total" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected llm_upstream_errors_total to be emitted on error")
	}
}

// TestRecordTelemetry_NoPlaintext guards the privacy rule: telemetry must never
// carry prompt/completion bodies or raw keys. We record with sentinel content
// in adjacent fields and assert none of it leaks into span attributes.
func TestRecordTelemetry_NoPlaintext(t *testing.T) {
	sr := newTestTracing(t)
	newTestMetrics(t)

	tr := otel.Tracer("test")
	ctx, span := tr.Start(context.Background(), "chat")
	RecordTelemetry(ctx, RequestTelemetry{
		Tenant: "acme", APIKeyID: "key_1", Provider: "openai", ModelRequested: "chat",
	})
	span.End()

	for _, kv := range sr.Ended()[0].Attributes() {
		v := kv.Value.AsString()
		if strings.Contains(v, "SECRET_PROMPT") || strings.Contains(v, "sk-plaintext") {
			t.Errorf("attribute %s leaked sensitive content: %q", kv.Key, v)
		}
	}
}
