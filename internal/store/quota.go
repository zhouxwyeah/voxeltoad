package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"voxeltoad/internal/billing"
)

// QuotaRepo is the PostgreSQL implementation of billing.QuotaStore (ADR-0013/
// 0016). Quota is money, so it is the data plane's one synchronous stateful
// dependency: balance mutations are single atomic SQL statements, and the
// multi-scope reservation is one transaction (all-or-nothing). Raw SQL on the
// hot path — not gorm's reflection/session machinery (ADR-0016).
type QuotaRepo struct {
	db *DB
}

// NewQuotaRepo builds a QuotaRepo over the given connection.
func NewQuotaRepo(db *DB) *QuotaRepo { return &QuotaRepo{db: db} }

// SetBalance upserts a scope's balance in micro-units (admin/test setup). A row
// existing is what makes a scope "configured" (and thus limited); absence means
// unlimited.
func (r *QuotaRepo) SetBalance(ctx context.Context, scope string, balance int64, currency string) error {
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO quotas (scope, balance, currency, updated_at)
		 VALUES (?, ?, ?, now())
		 ON CONFLICT (scope) DO UPDATE SET balance = EXCLUDED.balance,
		     currency = EXCLUDED.currency, updated_at = now()`,
		scope, balance, currency,
	).Error
}

// Balance reads a scope's current balance (test/admin inspection). Returns 0 for
// an unconfigured scope.
func (r *QuotaRepo) Balance(ctx context.Context, scope string) (int64, error) {
	var balance int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT balance FROM quotas WHERE scope = ?`, scope,
	).Scan(&balance).Error
	return balance, err
}

// BalanceWithCurrency reads a scope's current balance AND its currency label.
// Returns (0, "", nil) for an unconfigured scope (gorm scans a zero row without
// error, but the currency will remain its zero value). Callers that need to
// distinguish unconfigured from a legitimate zero-balance row can check
// currency == "" (quota rows always carry a non-empty currency label).
func (r *QuotaRepo) BalanceWithCurrency(ctx context.Context, scope string) (int64, string, error) {
	var row struct {
		Balance  int64
		Currency string
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT balance, currency FROM quotas WHERE scope = ?`, scope,
	).Scan(&row).Error
	return row.Balance, row.Currency, err
}

// TopUp atomically adds delta micro-units to a scope's balance, creating the
// row at delta when absent (ADR-0019). Unlike SetBalance, which overwrites, this
// is a currency-checked increment (balance = balance + delta) and so can
// run concurrently with hot-path TryDebit/Settle without losing updates — a
// top-up never clobbers a debit that landed between read and write, because
// there is no read. Use this for admin credit; use SetBalance only for
// authoritative resets.
func (r *QuotaRepo) TopUp(ctx context.Context, scope string, delta int64, currency string) error {
	if strings.HasPrefix(scope, "group:") {
		if _, _, err := billing.ParseGroupScope(scope); err != nil {
			return err
		}
	}
	currency = strings.ToLower(currency)
	if !validCurrency(currency) {
		return fmt.Errorf("%w: invalid quota currency", billing.ErrInvalidPolicy)
	}
	res := r.db.WithContext(ctx).Exec(
		`INSERT INTO quotas (scope, balance, currency, updated_at)
		 VALUES (?, ?, ?, now())
		 ON CONFLICT (scope) DO UPDATE SET balance = quotas.balance + EXCLUDED.balance,
		     updated_at = now() WHERE lower(quotas.currency) = EXCLUDED.currency`,
		scope, delta, currency,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("%w: quota %s", billing.ErrCurrencyMismatch, scope)
	}
	return nil
}

// TryDebit conditionally debits est from every configured scope in one
// transaction, all-or-nothing (ADR-0016). For each scope it runs the atomic
// conditional UPDATE; if that touches no row, it disambiguates insufficient
// balance (row exists → reject, roll back) from unconfigured (no row → skip,
// unlimited). Returns ok=false (no error) on insufficient balance.
func (r *QuotaRepo) TryDebit(ctx context.Context, scopes []string, est int64) (bool, error) {
	if est <= 0 || len(scopes) == 0 {
		return true, nil
	}
	ok := true
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, scope := range sortedQuotaScopes(scopes) {
			res := tx.Exec(
				`UPDATE quotas SET balance = balance - ?, updated_at = now()
				 WHERE scope = ? AND balance >= ?`,
				est, scope, est,
			)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				continue // debited
			}
			// No row updated: either the scope is unconfigured (unlimited → skip)
			// or it exists but is insufficient (reject + roll back).
			var exists bool
			if err := tx.Raw(
				`SELECT EXISTS (SELECT 1 FROM quotas WHERE scope = ?)`, scope,
			).Scan(&exists).Error; err != nil {
				return err
			}
			if exists {
				ok = false
				return errRollback // insufficient: undo any prior debits in this tx
			}
			// unconfigured → unlimited, skip
		}
		return nil
	})
	if errors.Is(err, errRollback) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Settle reconciles a prior reservation across the scope set by delta (refund
// when positive, extra charge when negative), one UPDATE per configured scope.
// Unconditional; unconfigured scopes are skipped (no row to update).
func (r *QuotaRepo) Settle(ctx context.Context, scopes []string, delta int64) error {
	if delta == 0 || len(scopes) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, scope := range sortedQuotaScopes(scopes) {
			if err := tx.Exec(
				`UPDATE quotas SET balance = balance + ?, updated_at = now()
				 WHERE scope = ?`,
				delta, scope,
			).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func sortedQuotaScopes(scopes []string) []string {
	out := append([]string(nil), scopes...)
	sort.Strings(out)
	n := 0
	for _, scope := range out {
		if n == 0 || out[n-1] != scope {
			out[n] = scope
			n++
		}
	}
	return out[:n]
}

// errRollback aborts a TryDebit transaction on insufficient balance without
// surfacing as a real error (the caller maps it to ok=false).
var errRollback = errors.New("quota: insufficient balance, rolling back")
