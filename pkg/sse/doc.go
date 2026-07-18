// Package sse provides helpers for parsing and writing Server-Sent Events, the
// streaming transport used by the OpenAI-compatible API and most upstream
// providers. It handles SSE framing (event boundaries on blank lines, partial
// frames spanning reads) so adapters can focus on provider-specific decoding.
//
// This package is part of L0 (pkg/) and MUST NOT import anything under
// internal/. See design/architecture.md.
package sse
