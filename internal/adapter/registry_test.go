package adapter

import (
	"context"
	"io"
	"testing"
)

// stubAdapter is a no-op Adapter used to exercise the registry.
type stubAdapter struct{ name string }

func (s stubAdapter) Name() string { return s.name }
func (s stubAdapter) BuildRequest(context.Context, *UnifiedRequest) (*UpstreamRequest, error) {
	return nil, nil
}
func (s stubAdapter) ParseResponse([]byte) (*UnifiedResponse, error) { return nil, nil }
func (s stubAdapter) ParseStream(io.Reader) (StreamReader, error)    { return nil, nil }
func (s stubAdapter) ExtractUsage(*UnifiedResponse) (*Usage, error)  { return nil, nil }

func TestRegistry_RegisterAndNew(t *testing.T) {
	// Use a unique name to avoid clashing with real adapters registered via
	// init() once they exist.
	const name = "stub-test-provider"
	Register(name, func(any) (Adapter, error) { return stubAdapter{name: name}, nil })

	a, err := New(name, nil)
	if err != nil {
		t.Fatalf("New(%q): %v", name, err)
	}
	if a.Name() != name {
		t.Errorf("Name() = %q, want %q", a.Name(), name)
	}
}

func TestRegistry_UnknownProvider(t *testing.T) {
	if _, err := New("does-not-exist", nil); err == nil {
		t.Error("expected error for unknown provider, got nil")
	}
}
