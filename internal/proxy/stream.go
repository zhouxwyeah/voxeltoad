package proxy

import (
	"errors"
	"io"
	"net/http"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/apperr"
	"voxeltoad/internal/ingress"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/plugin"
)

// streamChatCompletions relays a streaming completion to the client as SSE,
// using the ingress codec to translate each unified chunk into the client's
// wire format. It flushes after every chunk (so TTFT is preserved and nothing
// is buffered), and always terminates the client stream with the codec's
// terminator bytes — even if the upstream drops mid-stream — so clients never
// hang.
func streamChatCompletions(w http.ResponseWriter, r *http.Request, disp *Dispatcher, alias string, req *adapter.UnifiedRequest, chain *plugin.Chain, pc *plugin.Context, acc *telemetryAcc, codec ingress.Codec) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		acc.errType = apperr.StreamingUnsupported.Code
		writeCodecErr(w, codec, apperr.StreamingUnsupported.Status, apperr.StreamingUnsupported.Code, apperr.StreamingUnsupported.I18n)
		return
	}

	sr, dr, err := disp.ForwardStream(r.Context(), alias, req)
	if err != nil {
		// Nothing written yet, so we can still send a normal error response.
		status, typ := mapForwardError(err)
		acc.errType = typ
		acc.errMsg = truncate([]byte(err.Error()), 256)
		acc.setResult(dr, nil)
		acc.captureError(status, upstreamErrorBody(err))
		logForwardFailure(r, acc.requestID, acc.sessionID, req.Model, dr.Provider, typ, err)
		writeCodecErr(w, codec, status, typ, err.Error())
		return
	}
	defer func() { _ = sr.Close() }()

	w.Header().Set("Content-Type", codec.StreamContentType())
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Accumulate the usage seen on the stream so the completion hook can bill
	// what was actually received — even if the stream drops before the trailing
	// usage chunk (partial-stream billing, ADR-0012).
	var lastUsage *adapter.Usage
	firstChunk := true
	encoder := codec.NewStreamEncoder()

	// Always close the client stream with the codec's terminator and run the
	// completion hook, whatever happens below (normal end, drop, or client
	// disconnect).
	defer func() {
		if tail, err := encoder.Close(); err == nil && len(tail) > 0 {
			_, _ = w.Write(tail)
		}
		_, _ = w.Write(codec.StreamTerminator())
		flusher.Flush()
		runPost(chain, pc, &adapter.UnifiedResponse{Model: req.Model, Usage: lastUsage}, dr.Provider)
		acc.setResult(dr, lastUsage)
	}()

	for {
		chunk, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			// Upstream dropped or errored mid-stream. Headers (200) are already
			// sent, so we cannot change status; we stop relaying and the
			// deferred terminator + completion hook still run on what was
			// received.
			return
		}

		// TTFT: time from request entry to the first relayed chunk (ADR:
		// design/observability.md — measured on the first successful Recv).
		if firstChunk {
			acc.ttft = time.Since(acc.start)
			firstChunk = false
		}

		if chunk.Usage != nil {
			lastUsage = chunk.Usage
		}

		wire, encErr := encoder.EncodeChunk(chunk)
		if encErr != nil {
			// Headers (200) are already sent, so we cannot change status; we
			// stop relaying and the deferred terminator + completion hook still
			// run on what was received. Record the failure in telemetry + logs
			// so operators can diagnose truncated streams (previously this
			// path silently swallowed the error and the client saw a stream
			// that ended with the terminator, indistinguishable from success).
			acc.errType = "api_error"
			acc.errMsg = truncate([]byte(encErr.Error()), 256)
			observability.Logger().Error("stream chunk encode failed",
				"request_id", acc.requestID,
				"session_id", acc.sessionID,
				"model", req.Model,
				"error", truncate([]byte(encErr.Error()), 256),
			)
			return
		}
		// Capture the full wire-frame transcript + finish reason for the trace
		// ledger (ADR-0039). Best-effort; a no-op when capture is disabled.
		acc.captureStreamChunk(wire, chunk.FinishReason)
		if _, wErr := w.Write(wire); wErr != nil {
			return // client went away
		}
		flusher.Flush()
	}
}
