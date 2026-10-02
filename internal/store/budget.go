package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"voxeltoad/internal/billing"
)

type BudgetRepo struct {
	db       *DB
	tenantID int64
	now      func() time.Time
}

func NewBudgetRepo(db *DB, tenantID int64) *BudgetRepo {
	return &BudgetRepo{db: db, tenantID: tenantID, now: time.Now}
}

// SetClock is intended for deterministic calendar-boundary tests. Set it before
// sharing the repository between goroutines.
func (r *BudgetRepo) SetClock(now func() time.Time) { r.now = now }

type budgetPolicyRow struct {
	ID, TenantID                                                             int64
	Name, ScopeKind, ScopeRef, Environment, Period, Timezone, Currency, Mode string
	LimitAmount                                                              int64
	Thresholds                                                               []byte
	Enabled                                                                  bool
	Version                                                                  int64
	CreatedAt                                                                time.Time
}

func (row budgetPolicyRow) policy() (billing.BudgetPolicy, error) {
	p := billing.BudgetPolicy{ID: row.ID, TenantID: row.TenantID, Enabled: row.Enabled, Version: row.Version, CreatedAt: row.CreatedAt,
		BudgetSpec: billing.BudgetSpec{Name: row.Name, ScopeKind: row.ScopeKind, ScopeRef: row.ScopeRef, Environment: row.Environment,
			Period: row.Period, Timezone: row.Timezone, Currency: row.Currency, Limit: row.LimitAmount, Mode: row.Mode, Thresholds: []int{}}}
	err := json.Unmarshal(row.Thresholds, &p.Thresholds)
	return p, err
}

func validCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, c := range currency {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func validateBudgetSpec(spec *billing.BudgetSpec) error {
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	spec.Currency = strings.ToLower(spec.Currency)
	if strings.TrimSpace(spec.Name) == "" || len(spec.Name) > 200 || spec.Limit < 0 || !validCurrency(spec.Currency) || (spec.Mode != "enforce" && spec.Mode != "soft") {
		return fmt.Errorf("%w: name, currency, limit or mode", billing.ErrInvalidPolicy)
	}
	if _, _, err := billing.PeriodBounds(time.Now(), spec.Period, spec.Timezone); err != nil {
		return err
	}
	if spec.ScopeKind == "application_env" {
		if spec.Environment != "dev" && spec.Environment != "staging" && spec.Environment != "prod" {
			return fmt.Errorf("%w: environment", billing.ErrInvalidPolicy)
		}
	} else if spec.Environment != "" {
		return fmt.Errorf("%w: environment requires application_env", billing.ErrInvalidPolicy)
	}
	if spec.ScopeKind == "tenant" && spec.ScopeRef != "" {
		return fmt.Errorf("%w: tenant scope_ref must be empty", billing.ErrInvalidPolicy)
	}
	if spec.Thresholds == nil {
		spec.Thresholds = []int{}
	}
	spec.Thresholds = append([]int{}, spec.Thresholds...)
	sort.Ints(spec.Thresholds)
	for i, threshold := range spec.Thresholds {
		if threshold < 1 || threshold > 100 || (i > 0 && threshold == spec.Thresholds[i-1]) {
			return fmt.Errorf("%w: thresholds must be distinct integer percentages", billing.ErrInvalidPolicy)
		}
	}
	return nil
}

func lockBudgetTenant(tx *gorm.DB, tenantID int64) error {
	var id int64
	if err := tx.Raw(`SELECT id FROM tenants WHERE id = ? FOR UPDATE`, tenantID).Scan(&id).Error; err != nil {
		return err
	}
	if id == 0 {
		return billing.ErrNotFound
	}
	return nil
}

func validateBudgetOwner(tx *gorm.DB, tenantID int64, spec billing.BudgetSpec) error {
	var exists bool
	switch spec.ScopeKind {
	case "tenant":
		return nil
	case "group", "application", "application_env":
		id, err := strconv.ParseInt(spec.ScopeRef, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != spec.ScopeRef {
			return fmt.Errorf("%w: scope_ref must be a canonical entity ID", billing.ErrInvalidPolicy)
		}
		query := `SELECT EXISTS (SELECT 1 FROM applications WHERE tenant_id = ? AND id = ?)`
		if spec.ScopeKind == "group" {
			query = `SELECT EXISTS (SELECT 1 FROM groups WHERE tenant_id = ? AND id = ?)`
		}
		if err := tx.Raw(query, tenantID, id).Scan(&exists).Error; err != nil {
			return err
		}
	case "key":
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM api_keys WHERE tenant_id = ? AND key_id = ?)`, tenantID, spec.ScopeRef).Scan(&exists).Error; err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: scope_kind", billing.ErrInvalidPolicy)
	}
	if !exists {
		return fmt.Errorf("%w: scope not owned by tenant", billing.ErrInvalidPolicy)
	}
	return nil
}

func (r *BudgetRepo) Create(ctx context.Context, spec billing.BudgetSpec) (billing.BudgetPolicy, error) {
	if err := validateBudgetSpec(&spec); err != nil {
		return billing.BudgetPolicy{}, err
	}
	var row budgetPolicyRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBudgetTenant(tx, r.tenantID); err != nil {
			return err
		}
		if err := validateBudgetOwner(tx, r.tenantID, spec); err != nil {
			return err
		}
		thresholds, err := json.Marshal(spec.Thresholds)
		if err != nil {
			return err
		}
		return tx.Raw(`INSERT INTO budget_policies (tenant_id,name,scope_kind,scope_ref,environment,period,timezone,currency,limit_amount,mode,thresholds)
			VALUES (?,?,?,?,?,?,?,?,?,?,?::jsonb) RETURNING *`, r.tenantID, spec.Name, spec.ScopeKind, spec.ScopeRef, spec.Environment, spec.Period, spec.Timezone, spec.Currency, spec.Limit, spec.Mode, string(thresholds)).Scan(&row).Error
	})
	if err != nil {
		return billing.BudgetPolicy{}, err
	}
	return row.policy()
}

func budgetPage(cursor string, limit int) (int64, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var after int64
	if cursor != "" {
		id, err := decodeIDCursor(cursor)
		if err != nil || id < 0 {
			return 0, 0, fmt.Errorf("%w: invalid cursor", billing.ErrInvalidPolicy)
		}
		after = id
	}
	return after, limit, nil
}

func (r *BudgetRepo) List(ctx context.Context, cursor string, limit int) ([]billing.BudgetPolicy, string, error) {
	after, limit, err := budgetPage(cursor, limit)
	if err != nil {
		return nil, "", err
	}
	var rows []budgetPolicyRow
	if err := r.db.WithContext(ctx).Raw(`SELECT * FROM budget_policies WHERE tenant_id = ? AND id > ? ORDER BY id LIMIT ?`, r.tenantID, after, limit+1).Scan(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		next = encodeIDCursor(rows[limit-1].ID)
		rows = rows[:limit]
	}
	out := make([]billing.BudgetPolicy, 0, len(rows))
	for _, row := range rows {
		p, err := row.policy()
		if err != nil {
			return nil, "", err
		}
		out = append(out, p)
	}
	return out, next, nil
}

func (r *BudgetRepo) Get(ctx context.Context, id int64) (billing.BudgetPolicy, error) {
	var row budgetPolicyRow
	if err := r.db.WithContext(ctx).Raw(`SELECT * FROM budget_policies WHERE tenant_id = ? AND id = ?`, r.tenantID, id).Scan(&row).Error; err != nil {
		return billing.BudgetPolicy{}, err
	}
	if row.ID == 0 {
		return billing.BudgetPolicy{}, billing.ErrNotFound
	}
	return row.policy()
}

func (r *BudgetRepo) Patch(ctx context.Context, id int64, patch billing.BudgetPatch) (billing.BudgetPolicy, error) {
	if patch.Version <= 0 || (patch.Limit != nil && *patch.Limit < 0) {
		return billing.BudgetPolicy{}, billing.ErrInvalidPolicy
	}
	var row budgetPolicyRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBudgetTenant(tx, r.tenantID); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT * FROM budget_policies WHERE tenant_id = ? AND id = ? FOR UPDATE`, r.tenantID, id).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == 0 {
			return billing.ErrNotFound
		}
		if row.Version != patch.Version {
			return billing.ErrReservationConflict
		}
		if patch.Limit != nil {
			row.LimitAmount = *patch.Limit
		}
		if patch.Enabled != nil {
			row.Enabled = *patch.Enabled
		}
		if err := tx.Raw(`UPDATE budget_policies SET limit_amount = ?, enabled = ?, version = version + 1 WHERE tenant_id = ? AND id = ? RETURNING *`, row.LimitAmount, row.Enabled, r.tenantID, id).Scan(&row).Error; err != nil {
			return err
		}
		// Only this calendar period changes; historical accounts stay frozen.
		start, _, err := billing.PeriodBounds(r.now(), row.Period, row.Timezone)
		if err != nil {
			return err
		}
		var accountID int64
		if err := tx.Raw(`UPDATE budget_accounts SET limit_amount = ? WHERE policy_id = ? AND period_start = ? RETURNING id`, row.LimitAmount, id, start).Scan(&accountID).Error; err != nil {
			return err
		}
		if accountID != 0 {
			return emitBudgetThresholds(tx, r.tenantID, accountID, "")
		}
		return nil
	})
	if err != nil {
		return billing.BudgetPolicy{}, err
	}
	return row.policy()
}

func (r *BudgetRepo) Accounts(ctx context.Context, policyID int64) ([]billing.BudgetAccount, error) {
	if _, err := r.Get(ctx, policyID); err != nil {
		return nil, err
	}
	out := []billing.BudgetAccount{}
	err := r.db.WithContext(ctx).Raw(`SELECT a.id,a.policy_id,a.period_start,a.period_end,a.currency,a.limit_amount AS "limit",a.reserved,a.committed,a.released,
		a.limit_amount-a.reserved-a.committed AS available FROM budget_accounts a JOIN budget_policies p ON p.id=a.policy_id WHERE p.tenant_id=? AND p.id=? ORDER BY a.period_start DESC`, r.tenantID, policyID).Scan(&out).Error
	return out, err
}

func (r *BudgetRepo) Events(ctx context.Context, cursor string, limit int) ([]billing.BudgetEvent, string, error) {
	after, limit, err := budgetPage(cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := []billing.BudgetEvent{}
	if err := r.db.WithContext(ctx).Raw(`SELECT * FROM budget_events WHERE tenant_id=? AND id>? ORDER BY id LIMIT ?`, r.tenantID, after, limit+1).Scan(&out).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		next = encodeIDCursor(out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

func (r *BudgetRepo) Reservations(ctx context.Context, status, cursor string, limit int) ([]billing.Reservation, string, error) {
	if status != "" && status != "reserved" && status != "dispatched" && status != "unknown" && status != "settled" && status != "released" && status != "released_unknown" {
		return nil, "", billing.ErrInvalidPolicy
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	after := ""
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", billing.ErrInvalidPolicy
		}
		after = string(b)
	}
	rows := []reservationRow{}
	if err := r.db.WithContext(ctx).Raw(`SELECT * FROM billing_reservations WHERE tenant_id=? AND id>? AND (?='' OR status=?) ORDER BY id LIMIT ?`, r.tenantID, after, status, status, limit+1).Scan(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		next = base64.RawURLEncoding.EncodeToString([]byte(rows[limit-1].ID))
		rows = rows[:limit]
	}
	out := make([]billing.Reservation, 0, len(rows))
	for _, row := range rows {
		item, err := row.reservation()
		if err != nil {
			return nil, "", err
		}
		out = append(out, item)
	}
	return out, next, nil
}

func (r *BudgetRepo) Resolve(ctx context.Context, id string, res billing.Resolution) (billing.Reservation, error) {
	if res.Version <= 0 || res.OperatorID <= 0 || strings.TrimSpace(res.Reason) == "" || strings.TrimSpace(res.Evidence) == "" {
		return billing.Reservation{}, billing.ErrInvalidPolicy
	}
	if res.Action != "settle" && res.Action != "release" && res.Action != "release_unknown" {
		return billing.Reservation{}, billing.ErrInvalidPolicy
	}
	if (res.Action == "settle" && (res.Actual == nil || *res.Actual < 0)) || (res.Action != "settle" && res.Actual != nil) {
		return billing.Reservation{}, billing.ErrInvalidPolicy
	}
	var row reservationRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := shareAccountingTenant(tx, r.tenantID); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT * FROM billing_reservations WHERE tenant_id=? AND id=? FOR UPDATE`, r.tenantID, id).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			return billing.ErrNotFound
		}
		if row.Version != res.Version || len(row.Result) > 0 || (row.Status != "unknown" && (row.Status != "released_unknown" || res.Action != "settle")) {
			return billing.ErrReservationConflict
		}
		s := billing.Settlement{ReservationID: id, Outcome: "released", Reason: res.Reason}
		status := "released"
		if res.Action == "settle" {
			s.Outcome = "known"
			s.Actual = *res.Actual
			status = "settled"
		}
		if res.Action == "release_unknown" {
			status = "released_unknown"
		}
		if err := applyReservation(tx, row, s, status); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO budget_events (tenant_id,reservation_id,kind,amount,operator_id,reason,evidence) VALUES (?,?,?,?,?,?,?)`, r.tenantID, id, "resolution_"+res.Action, s.Actual, res.OperatorID, res.Reason, res.Evidence).Error; err != nil {
			return err
		}
		return tx.Raw(`SELECT * FROM billing_reservations WHERE tenant_id=? AND id=?`, r.tenantID, id).Scan(&row).Error
	})
	if err != nil {
		return billing.Reservation{}, err
	}
	return row.reservation()
}
