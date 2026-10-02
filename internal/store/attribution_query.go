package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AttributionQueryRepo binds migration reporting to one tenant; there is no
// global mode because the overview includes current API key counts.
type AttributionQueryRepo struct {
	db     *DB
	tenant string
}

func NewAttributionQueryRepo(db *DB, tenant string) *AttributionQueryRepo {
	return &AttributionQueryRepo{db: db, tenant: tenant}
}

type AttributionSummary struct {
	UnboundKeyCount           int64          `json:"unbound_key_count"`
	RequestCount              int64          `json:"request_count"`
	UnattributedRequestCount  int64          `json:"unattributed_request_count"`
	UnattributedRequestRatio  float64        `json:"unattributed_request_ratio"`
	RecordedUnattributedCosts []CurrencyCost `json:"recorded_unattributed_costs"`
	From                      *time.Time     `json:"from"`
	To                        *time.Time     `json:"to"`
}

// Summary counts current non-revoked keys missing any identity field (the same
// definition as ListAPIKeysFiltered), plus historical request/cost attribution
// in [from,to). Async ledgers may be incomplete; history is never backfilled.
func (r *AttributionQueryRepo) Summary(ctx context.Context, from, to time.Time) (AttributionSummary, error) {
	summary := AttributionSummary{RecordedUnattributedCosts: []CurrencyCost{}}
	if r.tenant == "" {
		return summary, fmt.Errorf("attribution overview requires a tenant")
	}
	where := []string{"tenant = ?"}
	args := []any{r.tenant}
	if !from.IsZero() {
		summary.From = &from
		where = append(where, "created_at >= ?")
		args = append(args, from)
	}
	if !to.IsZero() {
		summary.To = &to
		where = append(where, "created_at < ?")
		args = append(args, to)
	}
	if err := r.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM api_keys k
		JOIN tenants t ON t.id = k.tenant_id
		WHERE t.name = ? AND k.revoked_at IS NULL
		AND (k.group_id IS NULL OR k.application_id IS NULL OR k.environment = '')`,
		r.tenant).Scan(&summary.UnboundKeyCount).Error; err != nil {
		return summary, err
	}
	var counts struct {
		RequestCount             int64
		UnattributedRequestCount int64
	}
	if err := r.db.WithContext(ctx).Raw(`SELECT COUNT(*) AS request_count,
		COUNT(*) FILTER (WHERE application_id IS NULL) AS unattributed_request_count
		FROM request_logs WHERE `+strings.Join(where, " AND "), args...).Scan(&counts).Error; err != nil {
		return summary, err
	}
	summary.RequestCount = counts.RequestCount
	summary.UnattributedRequestCount = counts.UnattributedRequestCount
	if summary.RequestCount > 0 {
		summary.UnattributedRequestRatio = float64(summary.UnattributedRequestCount) / float64(summary.RequestCount)
	}
	if err := r.db.WithContext(ctx).Raw(`SELECT currency, COALESCE(SUM(cost), 0) AS cost
		FROM usage_records WHERE `+strings.Join(where, " AND ")+`
		AND application_id IS NULL GROUP BY currency ORDER BY currency`, args...).Scan(&summary.RecordedUnattributedCosts).Error; err != nil {
		return summary, err
	}
	return summary, nil
}
