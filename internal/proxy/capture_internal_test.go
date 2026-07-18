package proxy

import (
	"errors"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

// settingsFn returns a settings source yielding the given trace settings, so
// tests can drive the hot-reloadable capture gate without a real config.Store.
func settingsFn(captureEnabled bool, maxBodyKB int) func() *config.GatewaySettings {
	s := &config.GatewaySettings{Trace: config.TraceSettings{
		CapturePayloadEnabled: captureEnabled,
		MaxBodyKB:             maxBodyKB,
	}}
	return func() *config.GatewaySettings { return s }
}

// TestCaptureDisabledIsNoOp verifies the capture methods short-circuit (zero
// allocation, no field writes) when CapturePayloadEnabled is false (ADR-0039).
// The trace payload fields must remain their zero values.
func TestCaptureDisabledIsNoOp(t *testing.T) {
	acc := newTelemetryAcc("m", false, "rid", "sid", "tid", settingsFn(false, 0))
	acc.captureRequest([]byte(`{"model":"m"}`), &adapter.UnifiedRequest{Model: "m"})
	acc.captureResponse(&adapter.UnifiedResponse{Raw: []byte(`{"id":"r"}`)}, 200)
	acc.captureStreamChunk([]byte("data: {}\n\n"), "stop")
	acc.captureError(502, []byte("upstream error body"))

	if len(acc.tracePL.requestRaw) != 0 || len(acc.tracePL.messages) != 0 ||
		len(acc.tracePL.responseRaw) != 0 || acc.tracePL.errorRaw != "" ||
		acc.tracePL.nMessages != 0 || acc.tracePL.statusCode != 0 {
		t.Errorf("capture methods wrote to tracePL when disabled: %+v", acc.tracePL)
	}
}

// TestCaptureEnabledPopulatesFields verifies the capture methods populate the
// trace payload fields when CapturePayloadEnabled is true.
func TestCaptureEnabledPopulatesFields(t *testing.T) {
	acc := newTelemetryAcc("m", false, "rid", "sid", "tid", settingsFn(true, 0))
	acc.captureRequest([]byte(`{"model":"m"}`), &adapter.UnifiedRequest{
		Model:    "m",
		Messages: []adapter.Message{{Role: adapter.RoleUser}},
	})
	acc.captureResponse(&adapter.UnifiedResponse{Raw: []byte(`{"id":"r"}`)}, 200)

	if string(acc.tracePL.requestRaw) != `{"model":"m"}` {
		t.Errorf("requestRaw = %s", acc.tracePL.requestRaw)
	}
	if acc.tracePL.nMessages != 1 {
		t.Errorf("nMessages = %d, want 1", acc.tracePL.nMessages)
	}
	if string(acc.tracePL.responseRaw) != `{"id":"r"}` {
		t.Errorf("responseRaw = %s", acc.tracePL.responseRaw)
	}
	if acc.tracePL.statusCode != 200 {
		t.Errorf("statusCode = %d", acc.tracePL.statusCode)
	}
}

// TestCaptureEnabledPopulatesStreamResponseRaw verifies that streaming chunks
// accumulate the verbatim SSE wire-frame transcript in responseRaw (ADR-0039).
// The transcript is not valid JSON, so it must be stored as TEXT, not JSONB.
func TestCaptureEnabledPopulatesStreamResponseRaw(t *testing.T) {
	acc := newTelemetryAcc("m", true, "rid", "sid", "tid", settingsFn(true, 0))
	frame1 := []byte("data: {\"id\":\"chunk-1\"}\n\n")
	frame2 := []byte("data: [DONE]\n\n")
	acc.captureStreamChunk(frame1, "")
	acc.captureStreamChunk(frame2, "stop")

	want := string(frame1) + string(frame2)
	if string(acc.tracePL.responseRaw) != want {
		t.Errorf("responseRaw = %q, want %q", acc.tracePL.responseRaw, want)
	}
	if acc.tracePL.stopReason != "stop" {
		t.Errorf("stopReason = %q, want stop", acc.tracePL.stopReason)
	}
	if acc.tracePL.statusCode != 200 {
		t.Errorf("statusCode = %d, want 200", acc.tracePL.statusCode)
	}
}

// TestCaptureMaxBodyBytesCapsBodies verifies MaxBodyKB truncates captured
// request/response bodies (ADR-0039). MaxBodyKB is in KB, so 1 → 1024-byte cap;
// a 2048-byte body is truncated to 1024.
func TestCaptureMaxBodyBytesCapsBodies(t *testing.T) {
	acc := newTelemetryAcc("m", false, "rid", "sid", "tid", settingsFn(true, 1)) // 1 KB cap
	big := make([]byte, 2048)
	acc.captureRequest(big, &adapter.UnifiedRequest{})
	acc.captureResponse(&adapter.UnifiedResponse{Raw: big}, 200)
	acc.captureError(500, big)

	if len(acc.tracePL.requestRaw) != 1024 {
		t.Errorf("requestRaw len = %d, want 1024 (capped)", len(acc.tracePL.requestRaw))
	}
	if len(acc.tracePL.responseRaw) != 1024 {
		t.Errorf("responseRaw len = %d, want 1024 (capped)", len(acc.tracePL.responseRaw))
	}
	if len(acc.tracePL.errorRaw) != 1024 {
		t.Errorf("errorRaw len = %d, want 1024 (capped)", len(acc.tracePL.errorRaw))
	}
}

// TestCaptureNilSettingsIsDisabled verifies a nil settings source (e.g. before
// the first snapshot fetch) leaves capture disabled — defensive default.
func TestCaptureNilSettingsIsDisabled(t *testing.T) {
	acc := newTelemetryAcc("m", false, "rid", "sid", "tid", nil)
	acc.captureRequest([]byte(`{"model":"m"}`), &adapter.UnifiedRequest{Model: "m"})
	if len(acc.tracePL.requestRaw) != 0 {
		t.Errorf("nil settings should disable capture; got requestRaw=%s", acc.tracePL.requestRaw)
	}
}

// TestUpstreamErrorBody extracts the raw upstream body from a wrapped error
// (ADR-0039, code-review Issue H).
func TestUpstreamErrorBody(t *testing.T) {
	// An upstreamError carrying a body.
	ue := &upstreamError{kind: errUpstream4xx, body: []byte(`{"error":"bad"}`)}
	if got := upstreamErrorBody(ue); string(got) != `{"error":"bad"}` {
		t.Errorf("upstreamErrorBody = %q, want {\"error\":\"bad\"}", got)
	}
	// A wrapped error still resolves via errors.As.
	wrapped := errors.Join(ue)
	if got := upstreamErrorBody(wrapped); string(got) != `{"error":"bad"}` {
		t.Errorf("upstreamErrorBody(wrapped) = %q", got)
	}
	// A non-upstream error yields nil.
	if got := upstreamErrorBody(errors.New("other")); got != nil {
		t.Errorf("upstreamErrorBody(non-upstream) = %q, want nil", got)
	}
}
