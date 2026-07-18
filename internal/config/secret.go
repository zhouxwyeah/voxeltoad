package config

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// SecretScheme resolves the scheme-specific part of an APIKeyRef into a
// plaintext secret. For "env://VAR" the resolver receives "VAR".
type SecretScheme func(rest string) (string, error)

var (
	secretMu      sync.RWMutex
	secretSchemes = map[string]SecretScheme{
		"env":   resolveEnv,
		"plain": func(s string) (string, error) { return s, nil },
	}
)

// RegisterSecretScheme registers a custom secret scheme (the extension point
// for future backends like "vault" or "kms"). It is safe for concurrent use and
// overrides any existing scheme of the same name.
func RegisterSecretScheme(scheme string, fn SecretScheme) {
	secretMu.Lock()
	defer secretMu.Unlock()
	secretSchemes[scheme] = fn
}

// ResolveSecret turns a Provider.APIKeyRef into a plaintext credential.
// Supported by default (see ADR-0003):
//   - "env://VAR"      → value of environment variable VAR
//   - "plain://secret" → "secret" verbatim (dev/test only)
//   - "bare-secret"    → used verbatim if it contains no "scheme://" prefix
//
// A ref that contains "://" with an unregistered scheme is an error (so a typo
// fails loudly instead of being misread as a literal key). The returned value
// is sensitive and MUST never be logged.
func ResolveSecret(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("config: empty api_key_ref")
	}
	scheme, rest, hasScheme := strings.Cut(ref, "://")
	if !hasScheme {
		// Bare literal (no scheme) — treat as plaintext for convenience.
		return ref, nil
	}
	secretMu.RLock()
	fn, ok := secretSchemes[scheme]
	secretMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("config: unknown secret scheme %q in api_key_ref", scheme)
	}
	return fn(rest)
}

func resolveEnv(varName string) (string, error) {
	v, ok := os.LookupEnv(varName)
	if !ok {
		return "", fmt.Errorf("config: env var %q referenced by api_key_ref is not set", varName)
	}
	return v, nil
}
