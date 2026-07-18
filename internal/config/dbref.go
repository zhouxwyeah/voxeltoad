package config

import "strings"

const (
	// DBProviderRefPrefix is the scheme prefix for encrypted credentials stored
	// in the provider_credentials table. The full ref is "db://provider/<name>".
	DBProviderRefPrefix = "db://provider/"
)

// DBProviderRef returns the secret reference string for a provider whose
// credential is stored in the encrypted database table.
func DBProviderRef(providerName string) string {
	return DBProviderRefPrefix + providerName
}

// ParseDBProviderRef extracts the provider name from a "db://provider/<name>"
// reference. The second result is false if ref does not use the database
// provider credential scheme.
func ParseDBProviderRef(ref string) (string, bool) {
	if !strings.HasPrefix(ref, DBProviderRefPrefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, DBProviderRefPrefix), true
}

// ParseDBProviderPath extracts the provider name from the path portion of a db
// credential reference (the string received after the "://" by a secret
// resolver). It expects the form "provider/<name>".
func ParseDBProviderPath(path string) (string, bool) {
	const prefix = "provider/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(path, prefix)
	if name == "" {
		return "", false
	}
	return name, true
}
