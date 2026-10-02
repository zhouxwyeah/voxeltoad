package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"voxeltoad/internal/billing"
)

// AccountingRepo is the single synchronous writer for legacy balance and
// recurring spend reservations. No transaction spans an upstream call.
type AccountingRepo struct {
	db  *DB
	now func() time.Time
}

var _ billing.AccountingStore = (*AccountingRepo)(nil)

func NewAccountingRepo(db *DB) *AccountingRepo { return &AccountingRepo{db: db, now: time.Now} }

func (r *AccountingRepo) SetClock(now func() time.Time) { r.now = now }

type reservationRow struct {
	ID                   string
	TenantID             int64
	RequestID            string
	Identity             []byte
	Status               string
	Actual               *int64
	Reason               string
	Version              int64
	Result               []byte
	ResultApplied        bool
	CreatedAt, UpdatedAt time.Time
}

func (row reservationRow) reservation() (billing.Reservation, error) {
	out := billing.Reservation{TenantID: row.TenantID, Status: row.Status, Actual: row.Actual, Reason: row.Reason, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	err := json.Unmarshal(row.Identity, &out.ReservationRequest)
	return out, err
}

type reservationItem struct {
	ID                                  int64
	QuotaScope                          *string
	AccountID                           *int64
	Reserved, Held, Committed, Released int64
}

func (r *AccountingRepo) Reserve(ctx context.Context, req billing.ReservationRequest) error {
	req.Currency = strings.ToLower(req.Currency)
	if req.ID == "" || req.Tenant == "" || req.Estimate < 0 || !validCurrency(req.Currency) {
		return fmt.Errorf("%w: invalid reservation identity, estimate or currency", billing.ErrInvalidPolicy)
	}
	if len(req.PriceSnapshot) == 0 {
		req.PriceSnapshot = json.RawMessage(`{}`)
	}
	identity, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%w: price snapshot", billing.ErrInvalidPolicy)
	}
	now := r.now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tenantID int64
		// Shared tenant locks permit concurrent requests; policy mutations take
		// the exclusive counterpart before changing the applicable policy set.
		if err := tx.Raw(`SELECT id FROM tenants WHERE name=? FOR SHARE`, req.Tenant).Scan(&tenantID).Error; err != nil {
			return err
		}
		if tenantID == 0 {
			return fmt.Errorf("%w: tenant", billing.ErrNotFound)
		}
		insert := tx.Exec(`INSERT INTO billing_reservations (id,tenant_id,request_id,identity,status,created_at,updated_at) VALUES (?,?,?,?::jsonb,'reserved',?,?) ON CONFLICT (id) DO NOTHING`, req.ID, tenantID, req.RequestID, string(identity), now, now)
		if insert.Error != nil {
			return insert.Error
		}
		var row reservationRow
		if err := tx.Raw(`SELECT * FROM billing_reservations WHERE id=? FOR UPDATE`, req.ID).Scan(&row).Error; err != nil {
			return err
		}
		if insert.RowsAffected == 0 {
			var same bool
			if err := tx.Raw(`SELECT tenant_id=? AND identity=?::jsonb FROM billing_reservations WHERE id=?`, tenantID, string(identity), req.ID).Scan(&same).Error; err != nil {
				return err
			}
			if !same {
				return billing.ErrReservationConflict
			}
			return nil
		}
		var groupID int64
		if req.Group != "" {
			if err := tx.Raw(`SELECT id FROM groups WHERE tenant_id=? AND name=?`, tenantID, req.Group).Scan(&groupID).Error; err != nil {
				return err
			}
			if groupID == 0 {
				return fmt.Errorf("%w: group not owned by tenant", billing.ErrInvalidPolicy)
			}
		}
		if req.ApplicationID != nil {
			var exists bool
			if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM applications WHERE tenant_id=? AND id=?)`, tenantID, *req.ApplicationID).Scan(&exists).Error; err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: application not owned by tenant", billing.ErrInvalidPolicy)
			}
		}
		if req.APIKeyID != "" {
			var exists bool
			if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM api_keys WHERE tenant_id=? AND key_id=?)`, tenantID, req.APIKeyID).Scan(&exists).Error; err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: key not owned by tenant", billing.ErrInvalidPolicy)
			}
		}
		scopes := []string{"tenant:" + req.Tenant}
		if req.Group != "" {
			// Legacy unqualified groups have no trustworthy tenant ownership.
			var ambiguous bool
			if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM quotas WHERE scope=?)`, "group:"+req.Group).Scan(&ambiguous).Error; err != nil {
				return err
			}
			if ambiguous {
				return fmt.Errorf("%w: legacy group quota needs explicit tenant migration", billing.ErrInvalidPolicy)
			}
			scopes = append(scopes, billing.GroupScope(req.Tenant, req.Group))
		}
		if req.APIKeyID != "" {
			scopes = append(scopes, "key:"+req.APIKeyID)
		}
		sort.Strings(scopes)
		for _, scope := range scopes {
			var quota struct {
				Scope, Currency string
				Balance         int64
			}
			if err := tx.Raw(`SELECT scope,currency,balance FROM quotas WHERE scope=? FOR UPDATE`, scope).Scan(&quota).Error; err != nil {
				return err
			}
			if quota.Scope == "" {
				continue
			}
			if !strings.EqualFold(quota.Currency, req.Currency) {
				return fmt.Errorf("%w: quota %s", billing.ErrCurrencyMismatch, scope)
			}
			debit := tx.Exec(`UPDATE quotas SET balance=balance-?,updated_at=now() WHERE scope=? AND balance>0 AND balance>=?`, req.Estimate, scope, req.Estimate)
			if debit.Error != nil {
				return debit.Error
			}
			if debit.RowsAffected != 1 {
				return fmt.Errorf("%w: quota %s", billing.ErrInsufficient, scope)
			}
			if err := tx.Exec(`INSERT INTO billing_reservation_items (reservation_id,quota_scope,reserved,held) VALUES (?,?,?,?)`, req.ID, scope, req.Estimate, req.Estimate).Error; err != nil {
				return err
			}
		}
		var policies []budgetPolicyRow
		var appID int64
		if req.ApplicationID != nil {
			appID = *req.ApplicationID
		}
		if err := tx.Raw(`SELECT * FROM budget_policies WHERE tenant_id=? AND enabled AND (
			(scope_kind='tenant') OR (scope_kind='group' AND scope_ref=?) OR
			(scope_kind='application' AND scope_ref=?) OR
			(scope_kind='application_env' AND scope_ref=? AND environment=?) OR
			(scope_kind='key' AND scope_ref=? AND ?<>'')) ORDER BY id`, tenantID, strconv.FormatInt(groupID, 10), strconv.FormatInt(appID, 10), strconv.FormatInt(appID, 10), req.Environment, req.APIKeyID, req.APIKeyID).Scan(&policies).Error; err != nil {
			return err
		}
		// A policy owns exactly one account for this request's instant. Policy-ID
		// order is also the canonical account lock order used by settlement.
		for _, policy := range policies {
			if policy.Currency != req.Currency {
				return fmt.Errorf("%w: budget %d", billing.ErrCurrencyMismatch, policy.ID)
			}
			start, end, err := billing.PeriodBounds(now, policy.Period, policy.Timezone)
			if err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO budget_accounts (policy_id,period_start,period_end,currency,limit_amount) VALUES (?,?,?,?,?) ON CONFLICT (policy_id,period_start) DO NOTHING`, policy.ID, start, end, req.Currency, policy.LimitAmount).Error; err != nil {
				return err
			}
			var accountID int64
			if err := tx.Raw(`SELECT id FROM budget_accounts WHERE policy_id=? AND period_start=? FOR UPDATE`, policy.ID, start).Scan(&accountID).Error; err != nil {
				return err
			}
			reserve := tx.Exec(`UPDATE budget_accounts SET reserved=reserved+? WHERE id=? AND (?='soft' OR (limit_amount::numeric-committed-reserved>0 AND limit_amount::numeric-committed-reserved>=?))`, req.Estimate, accountID, policy.Mode, req.Estimate)
			if reserve.Error != nil {
				return reserve.Error
			}
			if reserve.RowsAffected != 1 {
				return fmt.Errorf("%w: budget %d", billing.ErrInsufficient, policy.ID)
			}
			if err := tx.Exec(`INSERT INTO billing_reservation_items (reservation_id,account_id,reserved,held) VALUES (?,?,?,?)`, req.ID, accountID, req.Estimate, req.Estimate).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *AccountingRepo) MarkDispatched(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Exec(`UPDATE billing_reservations SET status='dispatched',version=version+1,updated_at=? WHERE id=? AND status='reserved' AND result IS NULL`, r.now(), id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	var row reservationRow
	if err := r.db.WithContext(ctx).Raw(`SELECT * FROM billing_reservations WHERE id=?`, id).Scan(&row).Error; err != nil {
		return err
	}
	if row.ID == "" {
		return billing.ErrNotFound
	}
	if row.Status == "dispatched" && len(row.Result) == 0 {
		return nil
	}
	return billing.ErrReservationConflict
}

func validateSettlement(s billing.Settlement) error {
	if s.ReservationID == "" || s.Actual < 0 || (s.Outcome != "known" && s.Outcome != "released" && s.Outcome != "unknown") || (s.Outcome != "known" && (s.Actual != 0 || s.Usage != nil)) {
		return fmt.Errorf("%w: settlement", billing.ErrInvalidPolicy)
	}
	return nil
}

func (r *AccountingRepo) Finish(ctx context.Context, s billing.Settlement) error {
	if err := validateSettlement(s); err != nil {
		return err
	}
	if s.Outcome == "unknown" {
		return r.markUnknown(ctx, s.ReservationID, s.Reason)
	}
	result, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Phase one commits the result even if applying the financial transaction
	// fails. A disconnected database here is an error, never a recorded result.
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row reservationRow
		if err := tx.Raw(`SELECT * FROM billing_reservations WHERE id=? FOR UPDATE`, s.ReservationID).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			return billing.ErrNotFound
		}
		if len(row.Result) > 0 {
			var same bool
			if err := tx.Raw(`SELECT result=?::jsonb FROM billing_reservations WHERE id=?`, string(result), s.ReservationID).Scan(&same).Error; err != nil {
				return err
			}
			if !same {
				return billing.ErrReservationConflict
			}
			return nil
		}
		if row.Status == "settled" || row.Status == "released" || (row.Status == "released_unknown" && s.Outcome != "known") {
			return billing.ErrReservationConflict
		}
		return tx.Exec(`UPDATE billing_reservations SET result=?::jsonb,result_applied=false,version=version+1,updated_at=? WHERE id=?`, string(result), r.now(), s.ReservationID).Error
	})
	if err != nil {
		return err
	}
	return r.applyPending(ctx, s.ReservationID)
}

// Settlement also takes the tenant shared lock before account locks: otherwise
// an event's tenant FK check could deadlock with policy PATCH (tenant → account).
func shareAccountingTenant(tx *gorm.DB, tenantID int64) error {
	var id int64
	if err := tx.Raw(`SELECT id FROM tenants WHERE id=? FOR SHARE`, tenantID).Scan(&id).Error; err != nil {
		return err
	}
	if id == 0 {
		return billing.ErrNotFound
	}
	return nil
}

func (r *AccountingRepo) applyPending(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tenantID int64
		if err := tx.Raw(`SELECT tenant_id FROM billing_reservations WHERE id=?`, id).Scan(&tenantID).Error; err != nil {
			return err
		}
		if err := shareAccountingTenant(tx, tenantID); err != nil {
			return err
		}
		var row reservationRow
		if err := tx.Raw(`SELECT * FROM billing_reservations WHERE id=? FOR UPDATE`, id).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			return billing.ErrNotFound
		}
		if row.ResultApplied {
			return nil
		}
		if len(row.Result) == 0 {
			return billing.ErrReservationConflict
		}
		var s billing.Settlement
		if err := json.Unmarshal(row.Result, &s); err != nil {
			return err
		}
		status := "settled"
		if s.Outcome == "released" {
			status = "released"
		}
		return applyReservation(tx, row, s, status)
	})
}

// applyReservation requires the reservation row lock. Items record cumulative
// debits and refunds, so a later known charge after risk-release debits only the
// newly established cost, never refunds the original reservation twice.
func applyReservation(tx *gorm.DB, row reservationRow, s billing.Settlement, status string) error {
	var items []reservationItem
	if err := tx.Raw(`SELECT i.* FROM billing_reservation_items i LEFT JOIN budget_accounts a ON a.id=i.account_id WHERE i.reservation_id=? ORDER BY i.quota_scope NULLS LAST,a.policy_id,a.period_start`, row.ID).Scan(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		// Avoid overflow in Go when combining bigint money. PostgreSQL numeric
		// checks the final bigint representation at the actual write boundary.
		if item.QuotaScope != nil {
			res := tx.Exec(`UPDATE quotas SET balance=(balance::numeric+?+?-?)::bigint,updated_at=now() WHERE scope=? AND lower(currency)=?`, item.Held, item.Committed, s.Actual, *item.QuotaScope, identityCurrency(row.Identity))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return fmt.Errorf("%w: missing or changed reserved quota", billing.ErrCurrencyMismatch)
			}
		} else {
			res := tx.Exec(`UPDATE budget_accounts SET reserved=reserved-?,committed=(committed::numeric+?-?)::bigint,released=(released::numeric+GREATEST(?::numeric+?-?,0))::bigint WHERE id=?`, item.Held, s.Actual, item.Committed, item.Held, item.Committed, s.Actual, *item.AccountID)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return billing.ErrNotFound
			}
			if err := emitBudgetThresholds(tx, row.TenantID, *item.AccountID, row.ID); err != nil {
				return err
			}
		}
		if err := tx.Exec(`UPDATE billing_reservation_items SET held=0,committed=?,released=(released::numeric+GREATEST(?::numeric+?-?,0))::bigint WHERE id=?`, s.Actual, item.Held, item.Committed, s.Actual, item.ID).Error; err != nil {
			return err
		}
	}
	// The known usage row is persisted inside the same transaction as the
	// financial application: result_applied flips to true only when both land,
	// and the durable result carries the Usage payload so a crash between the
	// phases is replayed exactly once by the recovery loop (usage included).
	if s.Outcome == "known" && s.Usage != nil {
		if err := insertUsageRecord(tx, *s.Usage); err != nil {
			return err
		}
	}
	if s.AttemptRisk {
		if err := tx.Exec(`INSERT INTO budget_events (tenant_id,reservation_id,kind,reason) VALUES (?,?,'attempt_risk',?)`,
			row.TenantID, row.ID, "an earlier retryable upstream attempt may have consumed unreported tokens").Error; err != nil {
			return err
		}
	}
	if status == "released_unknown" {
		return tx.Exec(`UPDATE billing_reservations SET status=?,actual=NULL,reason=?,version=version+1,updated_at=now() WHERE id=?`, status, s.Reason, row.ID).Error
	}
	result, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return tx.Exec(`UPDATE billing_reservations SET status=?,actual=?,reason=?,result=?::jsonb,result_applied=true,version=version+1,updated_at=now() WHERE id=?`, status, s.Actual, s.Reason, string(result), row.ID).Error
}

func identityCurrency(identity []byte) string {
	var req billing.ReservationRequest
	if err := json.Unmarshal(identity, &req); err != nil {
		return ""
	}
	return req.Currency
}

func emitBudgetThresholds(tx *gorm.DB, tenantID, accountID int64, reservationID string) error {
	var reservationRef any
	if reservationID != "" {
		reservationRef = reservationID
	}
	if err := tx.Exec(`INSERT INTO budget_events (tenant_id,account_id,reservation_id,kind,threshold,amount)
		SELECT ?,a.id,?,'threshold',t.value::integer,a.committed FROM budget_accounts a JOIN budget_policies p ON p.id=a.policy_id
		CROSS JOIN LATERAL jsonb_array_elements_text(p.thresholds) t(value)
		WHERE a.id=? AND a.committed>0 AND a.committed::numeric*100 >= a.limit_amount::numeric*t.value::integer ON CONFLICT DO NOTHING`, tenantID, reservationRef, accountID).Error; err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO budget_events (tenant_id,account_id,reservation_id,kind,amount)
		SELECT ?,id,?,'overspend',committed-limit_amount FROM budget_accounts WHERE id=? AND committed>limit_amount ON CONFLICT DO NOTHING`, tenantID, reservationRef, accountID).Error
}

func (r *AccountingRepo) markUnknown(ctx context.Context, id, reason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tenantID int64
		if err := tx.Raw(`SELECT tenant_id FROM billing_reservations WHERE id=?`, id).Scan(&tenantID).Error; err != nil {
			return err
		}
		if err := shareAccountingTenant(tx, tenantID); err != nil {
			return err
		}
		var row reservationRow
		if err := tx.Raw(`SELECT * FROM billing_reservations WHERE id=? FOR UPDATE`, id).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			return billing.ErrNotFound
		}
		if row.Status == "unknown" || row.Status == "released_unknown" {
			return nil
		}
		if row.Status != "reserved" && row.Status != "dispatched" || len(row.Result) > 0 {
			return billing.ErrReservationConflict
		}
		return markReservationUnknown(tx, row, reason, r.now())
	})
}

func markReservationUnknown(tx *gorm.DB, row reservationRow, reason string, now time.Time) error {
	if err := tx.Exec(`UPDATE billing_reservations SET status='unknown',reason=?,version=version+1,updated_at=? WHERE id=?`, reason, now, row.ID).Error; err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO budget_events (tenant_id,reservation_id,kind,reason) VALUES (?,?,'unknown',?) ON CONFLICT DO NOTHING`, row.TenantID, row.ID, reason).Error
}

// MarkStale never refunds. A crashed process might have dispatched immediately
// before persisting its dispatch marker; neither state proves zero usage.
func (r *AccountingRepo) MarkStale(ctx context.Context, before time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []reservationRow
		if err := tx.Raw(`SELECT id,tenant_id FROM billing_reservations WHERE status IN ('reserved','dispatched') AND updated_at<? AND result IS NULL ORDER BY id LIMIT 500`, before).Scan(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}
		ids := make([]string, 0, len(candidates))
		tenantSet := map[int64]bool{}
		for _, row := range candidates {
			ids = append(ids, row.ID)
			tenantSet[row.TenantID] = true
		}
		tenants := make([]int64, 0, len(tenantSet))
		for id := range tenantSet {
			tenants = append(tenants, id)
		}
		sort.Slice(tenants, func(i, j int) bool { return tenants[i] < tenants[j] })
		for _, id := range tenants {
			if err := shareAccountingTenant(tx, id); err != nil {
				return err
			}
		}
		var rows []reservationRow
		if err := tx.Raw(`SELECT * FROM billing_reservations WHERE id IN ? AND status IN ('reserved','dispatched') AND updated_at<? AND result IS NULL ORDER BY id FOR UPDATE SKIP LOCKED`, ids, before).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := markReservationUnknown(tx, row, "stale reservation: charge unknown", r.now()); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *AccountingRepo) RetryPending(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var ids []string
	if err := r.db.WithContext(ctx).Raw(`SELECT id FROM billing_reservations WHERE result IS NOT NULL AND NOT result_applied ORDER BY updated_at,id LIMIT ?`, limit).Scan(&ids).Error; err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := r.applyPending(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("reservation %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
