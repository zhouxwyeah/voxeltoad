// Package app assembles the data plane's PostgreSQL-backed stores (auth keys,
// quota balances, usage records) and exposes them through the interfaces the
// proxy/billing layers depend on. It is the composition root that sits above
// internal/store and internal/billing; cmd/gateway wires it at startup.
//
// Quota is the data plane's one synchronous stateful dependency (ADR-0013);
// keys are looked up cache-first with this store as the fallback (ADR-0006);
// usage records are flushed asynchronously (fail-open, ADR-0016).
package app

import (
	"context"

	"voxeltoad/internal/auth"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/store"
)

// StoreOptions tunes the assembled stores.
type StoreOptions struct {
	// UsageBuffer is the async usage recorder's bounded buffer size. <=0 uses a
	// sensible default.
	UsageBuffer int
	// RequestLogBuffer is the async request-audit recorder's bounded buffer
	// size. <=0 uses a sensible default.
	RequestLogBuffer int
	// TracePayloadBuffer is the async trace-payload recorder's bounded buffer
	// size. <=0 uses a sensible default. The recorder is ALWAYS built and started
	// (ADR-0039): whether capture actually happens is gated per-request by the
	// hot-reloadable GatewaySettings, so flipping trace on/off needs no restart.
	TracePayloadBuffer int
}

// Stores holds the data plane's PG-backed dependencies, exposed through the
// consuming interfaces (so callers depend on behavior, not on concrete repos).
type Stores struct {
	// KeyStore is the authoritative API-key lookup (auth.Authenticator falls
	// back to it on cache miss).
	KeyStore auth.KeyStore
	// Quota is the strongly-consistent quota backend (pre-debit/settle).
	Quota billing.QuotaStore
	// UsageRecorder is the fail-open async usage recorder (started).
	UsageRecorder billing.UsageRecorder
	// RequestLog is the fail-open async request-audit recorder (started); the
	// proxy feeds it one row per request (request_logs ledger).
	RequestLog observability.RequestLogRecorder
	// TracePayload is the fail-open async trace-payload recorder (always started).
	// The proxy feeds it the message + raw layers per request (trace_payloads
	// ledger, ADR-0039). Whether capture actually happens is gated per-request by
	// the hot-reloadable GatewaySettings (trace.capture_payload_enabled).
	TracePayload observability.TracePayloadRecorder

	db         *store.DB
	quotaRep   *store.QuotaRepo
	recorder   *billing.AsyncRecorder
	reqLogRec  *observability.AsyncRequestLogRecorder
	tracePLRec *observability.AsyncTracePayloadRecorder
	cfgFresher func() bool
}

const (
	defaultUsageBuffer      = 1024
	defaultRequestLogBuffer = 1024
	defaultTracePLBuffer    = 1024
)

// OpenStores opens the database and builds the data-plane stores. The async
// usage recorder is started; call Close to drain it and close the connection.
func OpenStores(dsn string, opts StoreOptions) (*Stores, error) {
	db, err := store.Open(dsn)
	if err != nil {
		return nil, err
	}

	buf := opts.UsageBuffer
	if buf <= 0 {
		buf = defaultUsageBuffer
	}
	recorder := billing.NewAsyncRecorder(store.NewUsageRepo(db), buf)
	recorder.Start()

	rlBuf := opts.RequestLogBuffer
	if rlBuf <= 0 {
		rlBuf = defaultRequestLogBuffer
	}
	reqLogRec := observability.NewAsyncRequestLogRecorder(store.NewRequestLogRepo(db), rlBuf)
	reqLogRec.Start()

	// Trace-payload recorder (ADR-0039). ALWAYS built and started: whether
	// capture happens is gated per-request by the hot-reloadable GatewaySettings,
	// so the recorder running idle (when capture is off) costs one channel + one
	// goroutine and lets operators flip trace on/off from the admin UI without a
	// gateway restart.
	tplBuf := opts.TracePayloadBuffer
	if tplBuf <= 0 {
		tplBuf = defaultTracePLBuffer
	}
	tracePLRec := observability.NewAsyncTracePayloadRecorder(store.NewTracePayloadRepo(db), tplBuf)
	tracePLRec.Start()

	quotaRep := store.NewQuotaRepo(db)
	return &Stores{
		KeyStore:      store.NewKeyRepo(db),
		Quota:         quotaRep,
		UsageRecorder: recorder,
		RequestLog:    reqLogRec,
		TracePayload:  tracePLRec,
		db:            db,
		quotaRep:      quotaRep,
		recorder:      recorder,
		reqLogRec:     reqLogRec,
		tracePLRec:    tracePLRec,
	}, nil
}

// SetQuota configures a scope's balance (admin/seed helper; the data plane only
// debits/settles).
func (s *Stores) SetQuota(ctx context.Context, scope string, balance int64, currency string) error {
	return s.quotaRep.SetBalance(ctx, scope, balance, currency)
}

// DB exposes the underlying connection (admin/test use).
func (s *Stores) DB() *store.DB { return s.db }

// Ready reports whether the data plane is ready to serve traffic: a dynamic
// config snapshot has been fetched (non-empty version) AND the quota store's
// underlying connection can answer a trivial ping. Used by /readyz.
//
// Implements proxy.ReadinessProbe. The config-freshness check prevents an
// orchestrator from routing traffic to an instance whose admin plane is down
// (which would make /v1/chat/completions return 501 — while /healthz alone
// would still report 200).
func (s *Stores) Ready(ctx context.Context) bool {
	// Config freshness: a non-nil snapshot with a non-empty version means the
	// poller has successfully fetched at least once.
	if s.cfgFresher == nil || !s.cfgFresher() {
		return false
	}
	// Store reachability: ping the underlying pool. If the DB is unreachable
	// the quota store is down and chat would fail-closed anyway.
	sqlDB, err := s.db.DB.DB()
	if err != nil {
		return false
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return false
	}
	return true
}

// SetConfigFreshness wires a callback that reports whether dynamic config has
// been fetched at least once. Called from cmd/gateway after cfgStore is built.
func (s *Stores) SetConfigFreshness(f func() bool) { s.cfgFresher = f }

// Close drains the async recorders and closes the database connection.
func (s *Stores) Close() error {
	_ = s.recorder.Close()
	_ = s.reqLogRec.Close()
	if s.tracePLRec != nil {
		_ = s.tracePLRec.Close()
	}
	return s.db.Close()
}
