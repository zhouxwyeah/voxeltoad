package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Dynamic holds the hot-reloadable configuration owned by the admin plane and
// pulled by the data plane via the config snapshot. The typed sections
// (Providers/Models/Routes/Plugins) are the contract feature steps fill in;
// they are defined once here so later work only populates them and never has to
// reshape this struct (see design/architecture.md "配置即数据"). Raw is kept for
// forward-compatible passthrough of fields not yet modeled.
type Dynamic struct {
	// Version identifies this snapshot. The poller uses it to detect changes
	// (sent back to the admin plane as a conditional request).
	Version string `json:"version"`

	Providers []Provider     `json:"providers,omitempty"`
	Models    []Model        `json:"models,omitempty"`
	Routes    []Route        `json:"routes,omitempty"`
	Plugins   []PluginConfig `json:"plugins,omitempty"`

	// Settings are the gateway-wide behavior parameters (trace capture, future
	// otel/rate_limit). Hot-reloadable: the data plane applies them per-request
	// from Current(). nil means "no settings row" → callers use zero-value
	// defaults (e.g. trace capture off).
	Settings *GatewaySettings `json:"settings,omitempty"`

	// Raw carries provider-specific or not-yet-modeled config for forward
	// compatibility. It MUST NOT duplicate or override anything expressed by the
	// typed sections above — those are authoritative. Raw is only for fields the
	// typed schema does not cover (e.g. a provider's bespoke passthrough params).
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Store holds the current Dynamic config and swaps it atomically on update, so
// readers never observe a partially-applied change.
type Store struct {
	v atomic.Pointer[Dynamic]
}

// NewStore returns a Store seeded with an empty Dynamic.
func NewStore() *Store {
	s := &Store{}
	s.v.Store(&Dynamic{})
	return s
}

// Current returns the latest Dynamic snapshot. Safe for concurrent use.
func (s *Store) Current() *Dynamic { return s.v.Load() }

// Settings returns the current gateway behavior settings, or a zero-value
// GatewaySettings (all defaults, e.g. trace capture off) when no snapshot or no
// settings row is present. Safe for concurrent per-request use; never returns
// nil so callers can read fields without a nil check.
func (s *Store) Settings() *GatewaySettings {
	d := s.Current()
	if d == nil || d.Settings == nil {
		return &GatewaySettings{}
	}
	return d.Settings
}

// set atomically replaces the current snapshot.
func (s *Store) set(d *Dynamic) { s.v.Store(d) }

// Poller keeps a Store in sync with the admin plane by polling its
// config-snapshot endpoint. It sends the current version via If-None-Match so
// the admin plane can answer 304 Not Modified when nothing changed.
//
// This replaces a config center (etcd): config changes are low-frequency, so
// second-scale propagation is fine, and PostgreSQL stays the only stateful
// dependency. See design/architecture.md.
type Poller struct {
	adminURL      string
	interval      time.Duration
	store         *Store
	client        *http.Client
	internalToken string
}

// NewPoller builds a Poller bound to store. adminURL is the admin plane base
// URL; interval is the poll period.
func NewPoller(adminURL string, interval time.Duration, store *Store, opts ...PollerOption) *Poller {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	p := &Poller{
		adminURL: adminURL,
		interval: interval,
		store:    store,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// snapshotPath is the admin-plane endpoint serving the dynamic config snapshot.
const snapshotPath = "/internal/config/snapshot"

// InternalTokenHeader carries the shared secret authenticating the data
// plane ↔ management plane snapshot channel (ADR-0007). Defined here (L1) so
// both the poller and the admin plane reference one constant.
const InternalTokenHeader = "X-Internal-Token"

// PollerOption configures a Poller.
type PollerOption func(*Poller)

// WithInternalToken makes the poller send the shared internal-trust secret on
// every snapshot request (ADR-0007). Empty token = no header (open channel,
// dev/test only).
func WithInternalToken(token string) PollerOption {
	return func(p *Poller) { p.internalToken = token }
}

// Start performs an initial fetch and then polls until ctx is cancelled. The
// initial fetch is synchronous so startup fails fast if the admin plane is
// unreachable; the poll loop runs in a goroutine.
func (p *Poller) Start(ctx context.Context) error {
	if err := p.fetch(ctx); err != nil {
		return fmt.Errorf("config: initial snapshot fetch: %w", err)
	}
	go p.loop(ctx)
	return nil
}

func (p *Poller) loop(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Best-effort: transient errors are ignored; the next tick retries.
			_ = p.fetch(ctx)
		}
	}
}

func (p *Poller) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.adminURL+snapshotPath, nil)
	if err != nil {
		return err
	}
	if cur := p.store.Current(); cur != nil && cur.Version != "" {
		req.Header.Set("If-None-Match", cur.Version)
	}
	if p.internalToken != "" {
		req.Header.Set(InternalTokenHeader, p.internalToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil // config unchanged
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("config: snapshot returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var d Dynamic
	if err := json.Unmarshal(body, &d); err != nil {
		return fmt.Errorf("config: decode snapshot: %w", err)
	}
	if d.Version == "" {
		d.Version = resp.Header.Get("ETag")
	}
	p.store.set(&d)
	return nil
}
