// Package apperr provides a small, domain-partitioned error catalog for the
// management plane and data plane.
//
// Each error is a constant defined in a per-domain file (auth.go, tenant.go,
// provider.go, ...) carrying: a stable error code, an HTTP status, and the
// i18n key that resolves the user-facing message in web/src/locales/{en,zh}/
// errors/<domain>.json. Handlers return these instead of inlining
// errBody("error_type", "free text") at every call site.
//
// Why centralize: inline errors forced every worktree to edit the same
// crud.go / rbac.go hotspots and the single errors.json file, producing merge
// conflicts. With domain files, a worktree working on providers touches only
// apperr/provider.go and errors/provider.json.
//
// The on-wire shape is unchanged: {"error":{"message":<i18n value>,"type":<code>}}
// (see design/database.md, design/frontend.md §12).
package apperr

import "net/http"

// Error is a domain error: a stable code, an HTTP status, and the i18n key
// resolving the user-facing message. Errors are values, not sentinels — wrap
// one in an Errorf/WithErr to attach context, or return directly.
type Error struct {
	Code   string // stable machine-readable code, e.g. "tenant_not_found"
	Status int    // HTTP status code
	I18n   string // dotted i18n key, e.g. "errors.tenant.tenantNotFound"
}

// New constructs an Error. Keep codes snake_case and grouped by domain file.
func New(code string, status int, i18n string) *Error {
	return &Error{Code: code, Status: status, I18n: i18n}
}

// Error implements error.
func (e *Error) Error() string { return e.Code }

// Type returns the OpenAI-compatible error "type" value. We use the code
// itself as the type — the previous inline strings ("authentication_error",
// "invalid_request_error", ...) were ad-hoc and overlapped per domain. The
// OpenAPI Error schema (docs/openapi/admin.yaml) accepts any string here.
func (e *Error) Type() string { return e.Code }

// Common HTTP statuses used across domains — referenced here so domain files
// don't each import net/http.
const (
	StatusBadRequest          = http.StatusBadRequest
	StatusUnauthorized        = http.StatusUnauthorized
	StatusForbidden           = http.StatusForbidden
	StatusNotFound            = http.StatusNotFound
	StatusConflict            = http.StatusConflict
	StatusTooManyRequests     = http.StatusTooManyRequests
	StatusPaymentRequired     = http.StatusPaymentRequired
	StatusInternalServerError = http.StatusInternalServerError
	StatusBadGateway          = http.StatusBadGateway
)
