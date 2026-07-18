package config

import (
	"os"
	"testing"
)

func TestResolveSecret(t *testing.T) {
	t.Setenv("GATEWAY_TEST_KEY", "sk-from-env")

	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr bool
	}{
		{"env scheme", "env://GATEWAY_TEST_KEY", "sk-from-env", false},
		{"env missing var", "env://GATEWAY_DOES_NOT_EXIST", "", true},
		{"plain scheme", "plain://sk-literal", "sk-literal", false},
		{"bare literal", "sk-bare", "sk-bare", false},
		{"empty ref", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSecret(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ResolveSecret(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestRegisterSecretScheme_ExtensionPoint(t *testing.T) {
	RegisterSecretScheme("test-vault", func(path string) (string, error) {
		return "resolved:" + path, nil
	})
	got, err := ResolveSecret("test-vault://secret/openai")
	if err != nil {
		t.Fatalf("ResolveSecret: %v", err)
	}
	if got != "resolved:secret/openai" {
		t.Errorf("got %q, want resolved:secret/openai", got)
	}
}

// Guard: the bare-literal fallback must not swallow an unknown scheme that looks
// intentional (contains "://"), so a typo'd scheme fails loudly instead of
// being treated as a literal key.
func TestResolveSecret_UnknownSchemeErrors(t *testing.T) {
	if _, err := ResolveSecret("vault://secret/x"); err == nil {
		t.Error("unknown scheme should error, not be treated as a literal")
	}
	_ = os.Getenv // keep os imported if test trimmed
}
