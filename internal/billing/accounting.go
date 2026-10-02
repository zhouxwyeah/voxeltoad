package billing

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInsufficient        = errors.New("accounting: insufficient funds")
	ErrCurrencyMismatch    = errors.New("accounting: currency mismatch")
	ErrReservationConflict = errors.New("accounting: reservation state conflict")
	ErrInvalidPolicy       = errors.New("accounting: invalid budget policy")
	ErrNotFound            = errors.New("accounting: not found")
)

// ReservationRequest freezes authenticated identity and pricing before dispatch.
// ID is a fresh server-generated identifier, never a client idempotency key.
type ReservationRequest struct {
	ID            string          `json:"id"`
	RequestID     string          `json:"request_id"`
	Tenant        string          `json:"tenant"`
	Group         string          `json:"group"`
	APIKeyID      string          `json:"api_key_id"`
	ApplicationID *int64          `json:"application_id"`
	Environment   string          `json:"environment"`
	Currency      string          `json:"currency"`
	Estimate      int64           `json:"estimate"`
	PriceSnapshot json.RawMessage `json:"price_snapshot"`
}

type Settlement struct {
	ReservationID string `json:"reservation_id"`
	// Outcome is known, released (proven no charge), or unknown.
	Outcome string `json:"outcome"`
	Actual  int64  `json:"actual"`
	Reason  string `json:"reason"`
	// AttemptRisk marks a known settlement whose request had an earlier
	// retryable attempt that may have consumed unreported tokens (e.g. a
	// timeout that later failed over and succeeded). The store persists it as
	// a budget event; it never holds the reservation in unknown.
	AttemptRisk bool         `json:"attempt_risk,omitempty"`
	Usage       *UsageRecord `json:"usage,omitempty"`
}

// AccountingStore coordinates legacy quota and recurring accounts atomically.
type AccountingStore interface {
	Reserve(context.Context, ReservationRequest) error
	MarkDispatched(context.Context, string) error
	Finish(context.Context, Settlement) error
}

type BudgetSpec struct {
	Name      string `json:"name"`
	ScopeKind string `json:"scope_kind"`
	// ScopeRef is an entity id for group/application/application_env, a key_id
	// for key, and empty for tenant. The store validates tenant ownership.
	ScopeRef    string `json:"scope_ref"`
	Environment string `json:"environment"`
	Period      string `json:"period"`
	Timezone    string `json:"timezone"`
	Currency    string `json:"currency"`
	Limit       int64  `json:"limit"`
	Mode        string `json:"mode"`
	Thresholds  []int  `json:"thresholds"`
}

type BudgetPolicy struct {
	ID       int64 `json:"id"`
	TenantID int64 `json:"tenant_id"`
	BudgetSpec
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

type BudgetPatch struct {
	Version int64  `json:"version"`
	Limit   *int64 `json:"limit,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type BudgetAccount struct {
	ID          int64     `json:"id"`
	PolicyID    int64     `json:"policy_id"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Currency    string    `json:"currency"`
	Limit       int64     `json:"limit"`
	Reserved    int64     `json:"reserved"`
	Committed   int64     `json:"committed"`
	Released    int64     `json:"released"`
	Available   int64     `json:"available"`
}

type BudgetEvent struct {
	ID            int64     `json:"id"`
	TenantID      int64     `json:"tenant_id"`
	AccountID     *int64    `json:"account_id"`
	ReservationID string    `json:"reservation_id"`
	Kind          string    `json:"kind"`
	Threshold     int       `json:"threshold"`
	Amount        int64     `json:"amount"`
	OperatorID    *int64    `json:"operator_id"`
	Reason        string    `json:"reason"`
	Evidence      string    `json:"evidence"`
	CreatedAt     time.Time `json:"created_at"`
}

type Reservation struct {
	ReservationRequest
	TenantID  int64     `json:"tenant_id"`
	Status    string    `json:"status"`
	Actual    *int64    `json:"actual"`
	Reason    string    `json:"reason"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Resolution struct {
	Version int64 `json:"version"`
	// Action is settle, release (confirmed zero), or release_unknown.
	Action     string `json:"action"`
	Actual     *int64 `json:"actual,omitempty"`
	Reason     string `json:"reason"`
	Evidence   string `json:"evidence"`
	OperatorID int64  `json:"-"`
}
