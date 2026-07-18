package operator_test

import (
	"testing"

	"voxeltoad/internal/operator"
)

// argon2id password hashing: a hash verifies against the right password and
// rejects the wrong one; hashing is salted (two hashes of the same password
// differ).
func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := operator.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h == "" {
		t.Fatal("empty hash")
	}

	ok, err := operator.VerifyPassword("correct horse battery staple", h)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("correct password did not verify")
	}

	bad, err := operator.VerifyPassword("wrong password", h)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): %v", err)
	}
	if bad {
		t.Error("wrong password verified")
	}
}

func TestPasswordHashIsSalted(t *testing.T) {
	h1, _ := operator.HashPassword("same")
	h2, _ := operator.HashPassword("same")
	if h1 == h2 {
		t.Error("hashes of the same password are identical; salt missing")
	}
}

// A malformed/foreign hash string is a verification error, not a panic or a
// false positive.
func TestVerifyRejectsMalformedHash(t *testing.T) {
	ok, err := operator.VerifyPassword("x", "not-a-valid-phc-string")
	if err == nil {
		t.Error("expected error for malformed hash")
	}
	if ok {
		t.Error("malformed hash must not verify")
	}
}
