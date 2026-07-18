package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

// Forwarder forwards a unified request to a single upstream provider via its
// adapter and returns the unified response. It owns HTTP transport and the
// layered timeouts (connect / first-byte / overall); the adapter only
// translates (see design/architecture.md and ADR-0002). This step handles
// non-streaming only; streaming lands in step 3.5.
type Forwarder struct {
	adapter  adapter.Adapter
	timeouts config.ProviderTimeouts
	client   *http.Client
}

// NewForwarder builds a Forwarder for one adapter with the given layered
// timeouts. Connect bounds dialing; FirstByte bounds time-to-response-headers;
// Overall bounds the whole request (applied via context).
func NewForwarder(a adapter.Adapter, t config.ProviderTimeouts) *Forwarder {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: t.Connect}).DialContext,
		ResponseHeaderTimeout: t.FirstByte,
	}
	return &Forwarder{
		adapter:  a,
		timeouts: t,
		client:   &http.Client{Transport: transport},
	}
}

// Forward runs the non-streaming path: adapter.BuildRequest → send upstream
// (with overall-timeout context) → read body → adapter.ParseResponse.
func (f *Forwarder) Forward(ctx context.Context, req *adapter.UnifiedRequest) (*adapter.UnifiedResponse, error) {
	if f.timeouts.Overall > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeouts.Overall)
		defer cancel()
	}

	ur, err := f.adapter.BuildRequest(ctx, req)
	if err != nil {
		return nil, &upstreamError{kind: errBuild, err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, ur.Method, ur.URL, bytes.NewReader(ur.Body))
	if err != nil {
		return nil, &upstreamError{kind: errBuild, err: err}
	}
	httpReq.Header = ur.Header

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, &upstreamError{kind: classifyTransportErr(ctx, err), err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &upstreamError{kind: classifyTransportErr(ctx, err), err: err}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &upstreamError{
			kind: classifyUpstreamStatus(resp.StatusCode),
			err:  fmt.Errorf("upstream returned %d: %s", resp.StatusCode, truncate(body, 256)),
			body: body,
		}
	}

	unified, err := f.adapter.ParseResponse(body)
	if err != nil {
		// A 2xx body we cannot parse is a provider protocol fault; treat as a
		// retryable upstream failure so failover can try another provider.
		return nil, &upstreamError{kind: errUpstream5xx, err: err}
	}
	// Header overrides adapter body fallback: the response header is the
	// authoritative request-level id, while the body field may be a different
	// (completion-scoped) id on some providers.
	if hid := extractUpstreamID(resp.Header, f.adapter.Name()); hid != "" {
		unified.UpstreamRequestID = hid
	}
	return unified, nil
}

// truncate returns b as a string, capped at n bytes with an ellipsis appended
// when longer. Used to bound upstream error bodies in error messages/logs so a
// large or malicious upstream response can't blow up a log line.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// ForwardStream opens a streaming upstream request and returns a StreamReader
// of unified chunks. Unlike Forward, the overall timeout is NOT applied as a
// context deadline here: a long-lived stream would be killed by it. Connect and
// first-byte bounds still apply via the transport. The caller MUST Close the
// returned StreamReader, which also closes the upstream response body.
//
// The provider-assigned upstream request id (from the response header) is
// returned alongside the reader; empty when the provider returned no id.
func (f *Forwarder) ForwardStream(ctx context.Context, req *adapter.UnifiedRequest) (adapter.StreamReader, string, error) {
	ur, err := f.adapter.BuildRequest(ctx, req)
	if err != nil {
		return nil, "", &upstreamError{kind: errBuild, err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, ur.Method, ur.URL, bytes.NewReader(ur.Body))
	if err != nil {
		return nil, "", &upstreamError{kind: errBuild, err: err}
	}
	httpReq.Header = ur.Header

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, "", &upstreamError{kind: classifyTransportErr(ctx, err), err: err}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, "", &upstreamError{
			kind: classifyUpstreamStatus(resp.StatusCode),
			err:  fmt.Errorf("upstream returned %d: %s", resp.StatusCode, truncate(body, 256)),
			body: body,
		}
	}

	sr, err := f.adapter.ParseStream(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return nil, "", &upstreamError{kind: errUpstream5xx, err: err}
	}
	return &bodyClosingStream{StreamReader: sr, body: resp.Body}, extractUpstreamID(resp.Header, f.adapter.Name()), nil
}

// bodyClosingStream couples the adapter's StreamReader with the HTTP response
// body so Close releases both.
type bodyClosingStream struct {
	adapter.StreamReader
	body io.Closer
}

func (s *bodyClosingStream) Close() error {
	err := s.StreamReader.Close()
	if cerr := s.body.Close(); err == nil {
		err = cerr
	}
	return err
}

// extractUpstreamID reads the provider-assigned request id from an upstream
// response header. The header is the authoritative request-level correlation
// id; providers that also put it in the response body are handled by the
// adapter (as a fallback) before this runs. Returns "" when no id is present
// or the provider is unknown (unknown providers are silently ignored — they
// may not emit a request-level id at all).
func extractUpstreamID(h http.Header, provider string) string {
	switch provider {
	case "openai":
		return h.Get("x-request-id")
	case "claude", "anthropic":
		// Anthropic returns the id in both the `request-id` header and the
		// body's request_id field; the header wins.
		return h.Get("request-id")
	case "google", "gemini":
		return h.Get("x-goog-request-id")
	default:
		return ""
	}
}

// classifyTransportErr distinguishes timeouts (→ 504) from other upstream
// transport failures (→ 502). A timeout can surface either as the overall
// context deadline or as a transport-level timeout (e.g. ResponseHeaderTimeout
// for the first-byte bound), so both are checked.
func classifyTransportErr(ctx context.Context, err error) errKind {
	if ctx.Err() == context.DeadlineExceeded {
		return errTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errTimeout
	}
	// Non-timeout transport failures (connection refused/reset, DNS, etc.) are
	// transient upstream faults — retryable like a 5xx.
	return errUpstream5xx
}

// errKind classifies forwarding failures for HTTP status mapping and failover
// retry decisions (ADR-0011). Upstream HTTP failures are split into 4xx
// (non-retryable: moderation/malformed/auth/429) and 5xx (retryable).
type errKind int

const (
	errBuild errKind = iota
	errUpstream4xx
	errUpstream5xx
	errTimeout
)

// classifyUpstreamStatus maps an upstream HTTP status (>= 400) to its error
// kind.
func classifyUpstreamStatus(status int) errKind {
	if status >= 500 {
		return errUpstream5xx
	}
	return errUpstream4xx
}

type upstreamError struct {
	kind errKind
	err  error
	// body is the raw upstream HTTP response body when the upstream returned a
	// non-2xx status (nil for transport/build/parse errors that have no body).
	// Carried verbatim so the trace ledger can store the real upstream error body
	// (ADR-0039 error_raw) instead of the gateway-wrapped error string. Bounded
	// at capture time by the trace MaxBodyKB cap; the err field is still
	// truncated for log lines.
	body []byte
}

func (e *upstreamError) Error() string { return e.err.Error() }
func (e *upstreamError) Unwrap() error { return e.err }

// upstreamErrorBody extracts the raw upstream response body from err when it
// wraps an *upstreamError that carries one (a non-2xx upstream response). Returns
// nil for transport/build/parse errors that have no body. Used by the trace
// capture path so error_raw stores the real upstream body (ADR-0039), not the
// gateway-wrapped error string.
func upstreamErrorBody(err error) []byte {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return ue.body
	}
	return nil
}

// Retryable reports whether this failure may be retried on another provider
// (failover whitelist, ADR-0011): timeouts and upstream 5xx are retryable;
// build errors and upstream 4xx are not.
func (e *upstreamError) Retryable() bool {
	switch e.kind {
	case errTimeout, errUpstream5xx:
		return true
	default:
		return false
	}
}
