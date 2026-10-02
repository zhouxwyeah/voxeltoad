package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
)

func (p *Plugin) reserveAccounted(c *plugin.Context) error {
	fail := func(err error) error {
		c.Stop, c.BlockedBy = true, p.Name()
		c.RejectStatus = http.StatusServiceUnavailable
		if errors.Is(err, ErrInsufficient) {
			c.RejectStatus = http.StatusPaymentRequired
		}
		return nil
	}
	if c.Reserved < 0 {
		return fail(fmt.Errorf("invalid reservation estimate"))
	}
	var model *config.Model
	for _, m := range c.PricingSnapshot.Models {
		if m.Alias == c.Request.Model {
			copyModel := m
			model = &copyModel
			break
		}
	}
	if model == nil || len(model.Upstreams) == 0 {
		return fail(fmt.Errorf("model has no price"))
	}
	currency := ""
	for _, u := range model.Upstreams {
		cur := strings.ToLower(u.Pricing.Currency)
		if cur == "" || u.Pricing.PromptPer1M < 0 || u.Pricing.CompletionPer1M < 0 || u.Pricing.CacheHitMultiplier < 0 || u.Pricing.CacheHitMultiplier > 1_000_000 {
			return fail(fmt.Errorf("model has invalid pricing"))
		}
		if currency != "" && currency != cur {
			return fail(ErrCurrencyMismatch)
		}
		currency = cur
	}
	// Never persist Provider configuration: it can contain credential references
	// or development credentials. Only immutable non-secret pricing is needed.
	snapshot, err := json.Marshal(struct {
		Version string       `json:"version"`
		Model   config.Model `json:"model"`
	}{c.PricingSnapshot.Version, *model})
	if err != nil {
		return fail(err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fail(err)
	}
	reservationID := hex.EncodeToString(id[:])
	err = p.accounting.Reserve(c.Ctx, ReservationRequest{
		ID: reservationID, RequestID: c.RequestID,
		Tenant: c.Tenant, Group: c.Group, APIKeyID: c.APIKeyID,
		ApplicationID: c.ApplicationID, Environment: c.Environment,
		Currency: currency, Estimate: c.Reserved, PriceSnapshot: snapshot,
	})
	if err != nil {
		return fail(err)
	}
	c.ReservationID = reservationID
	c.BillingFinalize = func() error { return p.settle(c) }
	c.BeforeUpstream = func(ctx context.Context) error {
		return p.accounting.MarkDispatched(ctx, reservationID)
	}
	return nil
}
