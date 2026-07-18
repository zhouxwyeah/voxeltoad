package credential

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestAESGCMService_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	svc, err := NewAESGCMService(key)
	if err != nil {
		t.Fatalf("NewAESGCMService: %v", err)
	}

	plain := "sk-test-roundtrip-12345"
	cred, err := svc.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if cred.Algorithm != "AES-256-GCM" {
		t.Errorf("algorithm = %q, want AES-256-GCM", cred.Algorithm)
	}
	if cred.KeyVersion != "v0" {
		t.Errorf("key_version = %q, want v0", cred.KeyVersion)
	}
	if len(cred.Nonce) != 12 {
		t.Errorf("nonce length = %d, want 12", len(cred.Nonce))
	}

	got, err := svc.Decrypt(cred)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plain {
		t.Errorf("decrypted = %q, want %q", got, plain)
	}
}

func TestAESGCMService_NonceNotReused(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	svc, err := NewAESGCMService(key)
	if err != nil {
		t.Fatalf("NewAESGCMService: %v", err)
	}

	c1, _ := svc.Encrypt("plain")
	c2, _ := svc.Encrypt("plain")
	if bytes.Equal(c1.Nonce, c2.Nonce) {
		t.Error("nonce reused across two encryptions")
	}
	if bytes.Equal(c1.Ciphertext, c2.Ciphertext) {
		t.Error("ciphertext equal for same plaintext; nonce must be unique")
	}
}

func TestAESGCMService_WrongKeyFails(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = 0xff
	}

	svc1, _ := NewAESGCMService(key1)
	cred, _ := svc1.Encrypt("plain")

	svc2, _ := NewAESGCMService(key2)
	if _, err := svc2.Decrypt(cred); err == nil {
		t.Error("decrypt with wrong key should fail")
	}
}

func TestAESGCMService_BadKeyLength(t *testing.T) {
	if _, err := NewAESGCMService(make([]byte, 16)); err == nil {
		t.Error("16-byte key should be rejected")
	}
}

func TestDecodeBase64Key(t *testing.T) {
	valid := make([]byte, 32)
	for i := range valid {
		valid[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(valid)

	got, err := DecodeBase64Key(encoded)
	if err != nil {
		t.Fatalf("DecodeBase64Key: %v", err)
	}
	if !bytes.Equal(got, valid) {
		t.Error("decoded key mismatch")
	}

	if _, err := DecodeBase64Key(""); err == nil {
		t.Error("empty key should fail")
	}
	if _, err := DecodeBase64Key("bm90MzJieXRlcw=="); err == nil {
		t.Error("non-32-byte key should fail")
	}
}

func TestAESGCMService_AlgorithmMismatch(t *testing.T) {
	key := make([]byte, 32)
	svc, _ := NewAESGCMService(key)
	cred, _ := svc.Encrypt("plain")
	cred.Algorithm = "future-algo"

	if _, err := svc.Decrypt(cred); err == nil {
		t.Error("unsupported algorithm should fail")
	}
}
