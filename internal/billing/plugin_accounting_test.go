package billing_test

import (
	"context"
	"errors"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
)

type accountingProbe struct {
	reserved   []billing.ReservationRequest
	settled    []billing.Settlement
	reserveErr error
	finishErr  error
}

func (s *accountingProbe) Reserve(_ context.Context, r billing.ReservationRequest) error {
	if s.reserveErr != nil {
		return s.reserveErr
	}
	s.reserved = append(s.reserved, r)
	return nil
}
func (s *accountingProbe) MarkDispatched(context.Context, string) error { return nil }
func (s *accountingProbe) Finish(_ context.Context, r billing.Settlement) error {
	if s.finishErr != nil {
		return s.finishErr
	}
	s.settled = append(s.settled, r)
	return nil
}
func pricedSnapshot() *config.Dynamic {
	d := billingDyn()()
	for i := range d.Models[0].Upstreams {
		d.Models[0].Upstreams[i].Pricing.Currency = "usd"
	}
	return d
}

func TestAccountingPlugin_FreezesIdentityPriceAndFinishesOnce(t *testing.T) {
	store := &accountingProbe{}
	current := pricedSnapshot()
	recorder := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(func() *config.Dynamic { return current }, errQuotaStore{}, recorder, billing.WithAccounting(store))
	c := preCtx("acme", "chat")
	app := int64(23)
	c.ApplicationID, c.Environment, c.APIKeyID = &app, "prod", "key"
	c.RequestID = "shared-client-correlation"
	if err := runPhase(p, c, plugin.PhasePre); err != nil || c.Stop {
		t.Fatalf("reserve: %v %+v", err, c)
	}
	if c.BillingFinalize == nil || c.ReservationID == "" {
		t.Fatal("missing durable completion hook")
	}
	current = pricedSnapshot()
	current.Models[0].Upstreams[0].Pricing.PromptPer1M = 99_000_000
	c.Provider, c.ChargePossible = "openai", true
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 1000, CompletionTokens: 1000}}
	if err := runPhase(p, c, plugin.PhasePost); err != nil {
		t.Fatal(err)
	}
	if err := c.BillingFinalize(); err != nil {
		t.Fatal(err)
	}
	if len(store.settled) != 1 || store.settled[0].Actual != 20000 {
		t.Fatalf("settlements: %+v", store.settled)
	}
	if r := store.reserved[0]; r.ApplicationID == nil || *r.ApplicationID != app || r.Environment != "prod" {
		t.Fatalf("identity: %+v", r)
	}
	usage := store.settled[0].Usage
	if usage == nil || usage.Currency != "usd" || usage.ApplicationID == nil || usage.Cost != 20000 {
		t.Fatalf("settlement usage payload: %+v", usage)
	}
	// Accounting mode bypasses the fail-open async recorder: the ledger row is
	// persisted by the settlement transaction itself (and replayed from the
	// durable result on recovery), never through a lossy in-process buffer.
	if records := recorder.Records(); len(records) != 0 {
		t.Fatalf("accounting mode must not double-write the async ledger: %+v", records)
	}
}

// A failover that succeeds after a retryable failure settles the final usage
// as known; the earlier attempt's residual exposure is carried as AttemptRisk
// for the store to persist as an event, not as an unknown hold.
func TestAccountingPlugin_FailoverSettlesKnownWithAttemptRisk(t *testing.T) {
	store := &accountingProbe{}
	recorder := billing.NewMemoryUsageRecorder()
	p := billing.NewPlugin(pricedSnapshot, errQuotaStore{}, recorder, billing.WithAccounting(store))
	c := preCtx("acme", "chat")
	_ = runPhase(p, c, plugin.PhasePre)
	// Dispatcher signals: first attempt timed out, second attempt succeeded.
	c.ChargePossible, c.BillingAttemptRisk = true, true
	c.Provider = "openai"
	c.Response = &adapter.UnifiedResponse{Usage: &adapter.Usage{PromptTokens: 10, CompletionTokens: 5}}
	if err := c.BillingFinalize(); err != nil {
		t.Fatal(err)
	}
	if len(store.settled) != 1 || store.settled[0].Outcome != "known" || !store.settled[0].AttemptRisk {
		t.Fatalf("settlements: %+v", store.settled)
	}
	if usage := store.settled[0].Usage; usage == nil || usage.Cost != 125 {
		t.Fatalf("usage: %+v", usage)
	}
}

func TestAccountingPlugin_UnknownIsNotFree(t *testing.T) {
	for _, tc := range []struct {
		name            string
		sent, uncertain bool
		usage           *adapter.Usage
		want            string
	}{
		{"not dispatched", false, false, nil, "released"},
		{"missing usage", true, false, nil, "unknown"},
		{"partial usage", true, true, &adapter.Usage{PromptTokens: 10}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &accountingProbe{}
			rec := billing.NewMemoryUsageRecorder()
			p := billing.NewPlugin(pricedSnapshot, errQuotaStore{}, rec, billing.WithAccounting(s))
			c := preCtx("acme", "chat")
			_ = runPhase(p, c, plugin.PhasePre)
			c.Provider, c.ChargePossible, c.BillingUncertain = "openai", tc.sent, tc.uncertain
			c.Response = &adapter.UnifiedResponse{Usage: tc.usage}
			if err := c.BillingFinalize(); err != nil {
				t.Fatal(err)
			}
			if len(s.settled) != 1 || s.settled[0].Outcome != tc.want {
				t.Fatalf("settled: %+v", s.settled)
			}
			if len(rec.Records()) != 0 {
				t.Fatal("unknown must not create a zero-charge usage fact")
			}
		})
	}
}

func TestAccountingPlugin_RejectsMixedCurrencyAndPreservesPending(t *testing.T) {
	s := &accountingProbe{}
	d := pricedSnapshot()
	d.Models[0].Upstreams[1].Pricing.Currency = "cny"
	p := billing.NewPlugin(func() *config.Dynamic { return d }, errQuotaStore{}, billing.NewMemoryUsageRecorder(), billing.WithAccounting(s))
	c := preCtx("acme", "chat")
	_ = runPhase(p, c, plugin.PhasePre)
	if !c.Stop || len(s.reserved) != 0 {
		t.Fatal("mixed currencies must fail before reservation")
	}
	d = pricedSnapshot()
	c = preCtx("acme", "chat")
	_ = runPhase(p, c, plugin.PhasePre)
	s.finishErr = errors.New("database unavailable")
	if err := c.BillingFinalize(); err == nil || c.BillingDone {
		t.Fatal("failed finish must remain pending")
	}
	s.finishErr = nil
	if err := c.BillingFinalize(); err != nil || !c.BillingDone {
		t.Fatalf("retry: %v", err)
	}
}
