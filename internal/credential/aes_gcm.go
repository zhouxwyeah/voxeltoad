package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

const (
	aes256GCMKeyLen  = 32
	aesGCMNonceLen   = 12
	aesGCMAlgorithm  = "AES-256-GCM"
	aesGCMKeyVersion = "v0"
)

// AESGCMService encrypts/decrypts credentials with AES-256-GCM using a single
// 32-byte key. It is the P0 implementation; later key-encryption-key services
// (Vault, KMS) can implement the same Service interface.
type AESGCMService struct {
	key []byte
}

// NewAESGCMService builds an AES-256-GCM credential service from a 32-byte key.
func NewAESGCMService(key []byte) (*AESGCMService, error) {
	if len(key) != aes256GCMKeyLen {
		return nil, fmt.Errorf("credential/aesgcm: key length %d, want %d", len(key), aes256GCMKeyLen)
	}
	return &AESGCMService{key: append([]byte(nil), key...)}, nil
}

// Algorithm returns the algorithm name persisted with the ciphertext.
func (s *AESGCMService) Algorithm() string { return aesGCMAlgorithm }

// KeyVersion returns the current key version.
func (s *AESGCMService) KeyVersion() string { return aesGCMKeyVersion }

// Encrypt encrypts plaintext with AES-256-GCM.
func (s *AESGCMService) Encrypt(plaintext string) (Credential, error) {
	nonce, err := randomNonce(aesGCMNonceLen)
	if err != nil {
		return Credential{}, fmt.Errorf("credential/aesgcm: nonce generation failed: %w", err)
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return Credential{}, fmt.Errorf("credential/aesgcm: create cipher failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Credential{}, fmt.Errorf("credential/aesgcm: create GCM failed: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return Credential{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		Algorithm:  s.Algorithm(),
		KeyVersion: s.KeyVersion(),
	}, nil
}

// Decrypt decrypts a Credential with AES-256-GCM.
func (s *AESGCMService) Decrypt(cred Credential) (string, error) {
	if cred.Algorithm != "" && cred.Algorithm != aesGCMAlgorithm {
		return "", fmt.Errorf("credential/aesgcm: unsupported algorithm %q", cred.Algorithm)
	}
	if len(cred.Nonce) != aesGCMNonceLen {
		return "", fmt.Errorf("credential/aesgcm: nonce length %d, want %d", len(cred.Nonce), aesGCMNonceLen)
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", fmt.Errorf("credential/aesgcm: create cipher failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("credential/aesgcm: create GCM failed: %w", err)
	}

	plaintext, err := gcm.Open(nil, cred.Nonce, cred.Ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("credential/aesgcm: decrypt failed: %w", err)
	}
	return string(plaintext), nil
}
