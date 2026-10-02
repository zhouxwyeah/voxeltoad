package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
)

type accountingRecorder struct {
	settlements []billing.Settlement
}

func (*accountingRecorder) Reserve(context.Context, billing.ReservationRequest) error { return nil }
func (*accountingRecorder) MarkDispatched(context.Context, string) error              { return nil }
func (s *accountingRecorder) Finish(_ context.Context, result billing.Settlement) error {
	s.settlements = append(s.settlements, result)
	return nil
}

func TestAccountingHTTPRejectionsAndFailover(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout} {
		for _, body := range []string{"", `{"error":"upstream failure"}`} {
			for _, fallback := range []bool{false, true} {
				t.Run(fmt.Sprintf("status=%d/body=%t/fallback=%t", status, body != "", fallback), func(t *testing.T) {
					bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(status)
						_, _ = io.WriteString(w, body)
					}))
					defer bad.Close()
					good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = io.WriteString(w, `{"id":"ok","model":"chat","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
					}))
					defer good.Close()
					providers := []config.RouteProvider{{Name: "bad"}}
					if fallback {
						providers = append(providers, config.RouteProvider{Name: "good"})
					}
					d := NewDispatcher([]config.Route{{ModelAlias: "chat", Strategy: "priority", Providers: providers}},
						map[EndpointKey]*Forwarder{{Provider: "bad", Endpoint: "default"}: newTestForwarder(t, bad.URL), {Provider: "good", Endpoint: "default"}: newTestForwarder(t, good.URL)}, DispatcherConfig{})
					dyn := &config.Dynamic{Models: []config.Model{{Alias: "chat", Upstreams: []config.ModelUpstream{
						{Provider: "bad", Pricing: config.Pricing{Currency: "usd", CompletionPer1M: 2_000_000}},
						{Provider: "good", Pricing: config.Pricing{Currency: "usd", PromptPer1M: 1_000_000, CompletionPer1M: 2_000_000}},
					}}}}
					pc := &plugin.Context{Ctx: context.Background(), Tenant: "tenant", Request: &adapter.UnifiedRequest{Model: "chat"}, PricingSnapshot: dyn}
					pc.Ctx = context.WithValue(pc.Ctx, accountingContextKey{}, pc)
					accounting := &accountingRecorder{}
					usage := billing.NewMemoryUsageRecorder()
					bill := billing.NewPlugin(func() *config.Dynamic { return dyn }, nil, usage, billing.WithAccounting(accounting))
					if err := bill.Execute(pc, plugin.PhasePre); err != nil || pc.Stop {
						t.Fatalf("reserve: %v", err)
					}
					resp, dr, err := d.Forward(pc.Ctx, "chat", pc.Request)
					if fallback && status >= 500 {
						if err != nil || dr.Provider != "good" || resp.Usage == nil {
							t.Fatalf("fallback: %+v %v", dr, err)
						}
					} else if err == nil {
						t.Fatal("expected upstream rejection")
					}
					pc.Response, pc.Provider = resp, dr.Provider
					if err := pc.BillingFinalize(); err != nil {
						t.Fatal(err)
					}
					want := "unknown"
					switch {
					case status == http.StatusBadRequest:
						want = "released" // explicit rejection, provably no charge
					case fallback:
						// The failover succeeded with authoritative usage: settle
						// known; the failed 5xx attempt's residual exposure is
						// carried as a persisted attempt risk, not an unknown hold.
						want = "known"
					}
					if len(accounting.settlements) != 1 || accounting.settlements[0].Outcome != want {
						t.Fatalf("settlements=%+v, want %s", accounting.settlements, want)
					}
					if want == "known" && !accounting.settlements[0].AttemptRisk {
						t.Fatalf("successful failover must carry attempt risk: %+v", accounting.settlements[0])
					}
					if len(usage.Records()) != 0 {
						t.Fatal("accounting mode bypasses the async ledger; unconfirmed charge must not create usage")
					}
				})
			}
		}
	}
}

func TestAccountUpstreamResultMovesUncertaintyToRiskOnSuccess(t *testing.T) {
	pc := &plugin.Context{}
	ctx := context.WithValue(context.Background(), accountingContextKey{}, pc)
	accountUpstreamResult(ctx, &upstreamError{kind: errTimeout, err: errors.New("timeout")})
	if !pc.ChargePossible || !pc.BillingUncertain || pc.BillingAttemptRisk {
		t.Fatalf("uncertain attempt: %+v", pc)
	}
	accountUpstreamResult(ctx, nil)
	if !pc.ChargePossible || pc.BillingUncertain || !pc.BillingAttemptRisk {
		t.Fatalf("a successful attempt must settle its own usage and convert prior uncertainty into a recorded risk: %+v", pc)
	}
}

func TestAccountingSnapshotExcludesUnreachablePrices(t *testing.T) {
	dyn := &config.Dynamic{
		Version: "price-v1",
		Providers: []config.Provider{
			{Name: "usd", Endpoints: []config.ProviderEndpoint{{ID: "openai", Adapter: "openai", BaseURL: "http://localhost"}}},
			{Name: "cny", Endpoints: []config.ProviderEndpoint{{ID: "openai", Adapter: "openai", BaseURL: "http://localhost"}}},
		},
		Models: []config.Model{{Alias: "chat", Upstreams: []config.ModelUpstream{
			{Provider: "usd", Pricing: config.Pricing{Currency: "usd", CompletionPer1M: 1}},
			{Provider: "cny", Pricing: config.Pricing{Currency: "cny", CompletionPer1M: 999999}},
		}}},
		Routes: []config.Route{{ModelAlias: "chat", Providers: []config.RouteProvider{{Name: "usd"}}}},
	}
	d := NewDispatcher(dyn.Routes, map[EndpointKey]*Forwarder{{Provider: "usd", Endpoint: "openai"}: {}}, DispatcherConfig{}).WithModelPreparation(dyn)
	s := d.accountingSnapshot("chat", "openai")
	if s.Version != "price-v1" || len(s.Models) != 1 || len(s.Models[0].Upstreams) != 1 || s.Models[0].Upstreams[0].Provider != "usd" {
		t.Fatalf("billing snapshot includes unreachable upstream: %+v", s)
	}
	if len(dyn.Models[0].Upstreams) != 2 {
		t.Fatal("filtered pricing mutated live dispatcher config")
	}
}
