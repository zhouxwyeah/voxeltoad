// Package credential provides encryption and decryption for sensitive provider
// upstream credentials. It is intentionally independent of storage and secret
// resolution so that:
//
//   - internal/config can keep its lightweight scheme registry.
//   - internal/store can persist encrypted blobs without knowing the algorithm.
//   - cmd/gateway can compose the service with the store at startup.
//
// All plaintext values are handled as strings and are expected to be wiped
// from memory as soon as possible by callers (Go does not provide explicit
// memory zeroing, so we avoid retaining copies).
package credential

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// Credential is the encrypted form of a provider API key stored in PostgreSQL.
type Credential struct {
	ProviderName string
	Ciphertext   []byte
	Nonce        []byte
	Algorithm    string
	KeyVersion   string
}

// Service encrypts and decrypts provider credentials. Implementations must be
// safe for concurrent use.
type Service interface {
	// Encrypt turns a plaintext API key into an encrypted Credential.
	Encrypt(plaintext string) (Credential, error)
	// Decrypt recovers the plaintext API key from a Credential.
	Decrypt(cred Credential) (string, error)
	// Algorithm returns the algorithm name recorded in storage.
	Algorithm() string
	// KeyVersion returns the current key version string.
	KeyVersion() string
}

// DecodeBase64Key decodes a base64-encoded 32-byte key, typically supplied via
// the GATEWAY_PROVIDER_CREDENTIAL_KEK environment variable. It validates the
// length and returns a clear error for misconfiguration.
func DecodeBase64Key(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("credential: empty key")
	}
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("credential: key is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("credential: key length is %d, want 32 bytes", len(key))
	}
	return key, nil
}

// randomNonce returns a nonce of the given length using crypto/rand.
func randomNonce(n int) ([]byte, error) {
	nonce := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}
