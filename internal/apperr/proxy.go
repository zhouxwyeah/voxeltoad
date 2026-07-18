package apperr

// Data-plane (proxy) errors. These map to OpenAI-compatible error types in
// responses going back to API-key clients (not admin operators).
var (
	// Internal-token / gateway-internal errors.
	InvalidInternalToken = New("invalid_internal_token", StatusUnauthorized, "errors.proxy.invalidInternalToken")
	SnapshotFailed       = New("snapshot_failed", StatusInternalServerError, "errors.proxy.snapshotFailed")

	// Request-level errors at the proxy edge.
	InvalidRequestBody   = New("invalid_request_body", StatusBadRequest, "errors.proxy.invalidRequestBody")
	ModelNotPermitted    = New("model_not_permitted", StatusForbidden, "errors.proxy.modelNotPermitted")
	UpstreamUnreachable  = New("upstream_unreachable", StatusBadGateway, "errors.proxy.upstreamUnreachable")
	StreamingUnsupported = New("streaming_unsupported", StatusInternalServerError, "errors.proxy.streamingUnsupported")
	RequestBlocked       = New("request_blocked", StatusForbidden, "errors.proxy.requestBlocked")
)
