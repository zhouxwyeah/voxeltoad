// Package errors defines the gateway's error types and their mapping to
// OpenAI-compatible error responses, so clients see a consistent error shape
// regardless of which upstream provider produced the failure.
//
// This package is part of L0 (pkg/) and MUST NOT import anything under
// internal/. See design/architecture.md.
package errors
