// Package tokenizer provides token-counting helpers used for pre-request
// estimation and rate-limiting decisions. Billing itself MUST rely on the usage
// reported by the upstream response, not these local estimates (see
// design/e2e.md Pitfalls); tokenizers here are best-effort approximations.
//
// This package is part of L0 (pkg/) and MUST NOT import anything under
// internal/. See design/architecture.md.
package tokenizer
