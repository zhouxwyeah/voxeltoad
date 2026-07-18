package adapter

import (
	"fmt"
	"sync"
)

// Factory builds an Adapter from a provider configuration blob. The concrete
// config type is provider-specific; adapters type-assert as needed.
type Factory func(cfg any) (Adapter, error)

var (
	mu        sync.RWMutex
	factories = make(map[string]Factory)
)

// Register makes a provider adapter factory available under name. It is
// intended to be called from adapter packages' init functions. Registering the
// same name twice panics, since that indicates a programming error.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[name]; dup {
		panic(fmt.Sprintf("adapter: Register called twice for %q", name))
	}
	factories[name] = f
}

// New constructs a registered adapter by name with the given config.
func New(name string, cfg any) (Adapter, error) {
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("adapter: unknown provider %q", name)
	}
	return f(cfg)
}

// Registered returns the names of all registered adapters.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(factories))
	for n := range factories {
		names = append(names, n)
	}
	return names
}
