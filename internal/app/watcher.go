package app

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"voxeltoad/internal/config"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/proxy"

	// Register the built-in provider adapters so BuildDispatcher can construct
	// them by name from config (the composition root owns adapter registration).
	_ "voxeltoad/internal/adapter/claude"
	_ "voxeltoad/internal/adapter/openai"
)

// DispatcherWatcher builds a proxy.Dispatcher from the current dynamic config
// and rebuilds it (atomic swap) whenever the snapshot version changes. The data
// plane's router resolves the dispatcher per request via Current, so a rebuild
// takes effect without restart (the architecture's hot-reload model; see
// design/architecture.md). A failed rebuild keeps the last-good dispatcher.
type DispatcherWatcher struct {
	source func() *config.Dynamic // current snapshot (e.g. config.Store.Current)
	cfg    proxy.DispatcherConfig

	disp        atomic.Pointer[proxy.Dispatcher]
	mu          sync.Mutex // serializes rebuilds
	lastVersion string
}

// NewDispatcherWatcher builds a watcher reading config from source.
func NewDispatcherWatcher(source func() *config.Dynamic, cfg proxy.DispatcherConfig) *DispatcherWatcher {
	return &DispatcherWatcher{source: source, cfg: cfg}
}

// Current returns the latest successfully-built dispatcher (nil before the first
// successful Build). Safe for concurrent use; suitable as a
// proxy.DispatcherProvider.
func (w *DispatcherWatcher) Current() *proxy.Dispatcher { return w.disp.Load() }

// Build constructs the dispatcher from the current config synchronously, return
// the build error (used at startup to fail fast on bad initial config).
func (w *DispatcherWatcher) Build() error {
	return w.rebuild(true)
}

// Refresh rebuilds the dispatcher if the config version changed since the last
// successful build. A build error is logged and the last-good dispatcher is
// kept (never returned to the caller — Refresh is best-effort).
func (w *DispatcherWatcher) Refresh() {
	if err := w.rebuild(false); err != nil {
		observability.Logger().Warn("dispatcher rebuild failed; keeping last-good config", "err", err)
	}
}

func (w *DispatcherWatcher) rebuild(force bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	dyn := w.source()
	if dyn == nil {
		dyn = &config.Dynamic{}
	}
	if !force && dyn.Version == w.lastVersion && w.disp.Load() != nil {
		return nil // unchanged
	}

	disp, err := proxy.BuildDispatcher(dyn, w.cfg)
	if err != nil {
		return err // keep last-good (disp pointer untouched)
	}
	w.disp.Store(disp)
	w.lastVersion = dyn.Version
	return nil
}

// Watch polls for config changes at interval and rebuilds on version change,
// until ctx is cancelled. Run in a goroutine after an initial Build.
func (w *DispatcherWatcher) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Refresh()
		}
	}
}
