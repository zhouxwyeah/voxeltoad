package adapter_test

import (
	"testing"

	"voxeltoad/internal/adapter"

	// register the built-in adapters.
	_ "voxeltoad/internal/adapter/claude"
	_ "voxeltoad/internal/adapter/openai"
)

// The shared adapter.Options is the single config type every adapter factory
// accepts, so callers (the proxy assembler) construct any adapter generically
// via the registry without importing concrete adapter packages.
func TestNew_AcceptsSharedOptions(t *testing.T) {
	for _, name := range []string{"openai", "claude"} {
		t.Run(name, func(t *testing.T) {
			a, err := adapter.New(name, adapter.Options{BaseURL: "https://x", APIKey: "k"})
			if err != nil {
				t.Fatalf("New(%q, adapter.Options) error: %v", name, err)
			}
			if a == nil {
				t.Fatalf("New(%q) returned nil adapter", name)
			}
			if a.Name() != name {
				t.Errorf("Name() = %q, want %q", a.Name(), name)
			}
		})
	}
}

func TestNew_MissingBaseURL(t *testing.T) {
	if _, err := adapter.New("openai", adapter.Options{APIKey: "k"}); err == nil {
		t.Error("expected error when BaseURL is empty")
	}
}
