package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/store"
)

// mountUsage wires read-only usage endpoints for BOTH roles (ADR-0019):
// super-admin sees all tenants; a tenant-admin is scoped to its own tenant. The
// scope is resolved from the operator, never from the request, so a tenant-admin
// cannot read another tenant's usage. Reads are not audited (ADR-0017 §5).
func mountUsage(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	g.GET("/usage", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}
		repo := store.NewUsageQueryRepo(db, tenant)
		filter := store.UsageFilter{
			Provider: c.Query("provider"),
			Model:    c.Query("model"),
			From:     from,
			To:       to,
		}
		if c.Query("format") == "csv" {
			rows, _, err := repo.List(c.Request.Context(), filter, "", 2000)
			if err != nil {
				internalErr(c, err)
				return
			}
			exportUsageCSV(c, rows)
			return
		}
		rows, next, err := repo.List(c.Request.Context(), filter, c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(rows, next))
	})

	g.GET("/usage/summary", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}
		groupBy := c.DefaultQuery("group_by", "model")
		repo := store.NewUsageQueryRepo(db, tenant)
		rows, err := repo.Summary(c.Request.Context(), from, to, groupBy)
		if err != nil {
			// Summary rejects an unknown group_by; surface as 400.
			badRequest(c, err.Error())
			return
		}
		c.JSON(http.StatusOK, listEnvelope(rows, ""))
	})

	g.GET("/usage/timeseries", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}
		bucket := c.DefaultQuery("bucket", "day")
		repo := store.NewUsageQueryRepo(db, tenant)
		rows, err := repo.Timeseries(c.Request.Context(), store.UsageFilter{
			Provider: c.Query("provider"),
			Model:    c.Query("model"),
			From:     from,
			To:       to,
		}, bucket)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		c.JSON(http.StatusOK, listEnvelope(rows, ""))
	})
}

// usageTenantScope resolves the tenant name a usage query should be bound to
// from the operator: "" (global) for super-admin unless the request explicitly
// scopes to a valid tenant via ?tenant=NAME, and the operator's own tenant name
// for a tenant-admin. Any other role is rejected 403. It writes the error
// response itself and returns ok=false on failure.
func usageTenantScope(c *gin.Context, db *store.DB) (string, bool) {
	op := operatorFrom(c)
	// Phase-2 RBAC: scope is determined by whether the operator has a
	// tenant_id, NOT by the textual role name (which can now be any custom
	// role). A global-scope operator (tenant_id IS NULL) sees all tenants;
	// a tenant-scoped operator is restricted to its own tenant.
	if op.TenantID != nil {
		name, err := store.TenantName(c.Request.Context(), db, *op.TenantID)
		if err != nil {
			internalErr(c, err)
			return "", false
		}
		if name == "" {
			appErr(c, apperr.TenantNotFound)
			return "", false
		}
		return name, true
	}
	// Global scope: optional tenant filter via query param.
	if t := c.Query("tenant"); t != "" {
		exists, err := store.TenantExists(c.Request.Context(), db, t)
		if err != nil {
			internalErr(c, err)
			return "", false
		}
		if !exists {
			badRequest(c, "unknown tenant")
			return "", false
		}
		return t, true
	}
	return "", true
}

// parseTimeRange reads optional RFC3339 from/to query params. Absent params are
// zero times (unbounded). Malformed values → 400 (ok=false).
func parseTimeRange(c *gin.Context) (from, to time.Time, ok bool) {
	if s := c.Query("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			badRequest(c, "invalid from timestamp (want RFC3339)")
			return time.Time{}, time.Time{}, false
		}
		from = t
	}
	if s := c.Query("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			badRequest(c, "invalid to timestamp (want RFC3339)")
			return time.Time{}, time.Time{}, false
		}
		to = t
	}
	return from, to, true
}

// parseLimit reads an optional limit query param, clamped to [1, 500].
func parseLimit(c *gin.Context) int {
	const def, max = 50, 500
	s := c.Query("limit")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// Offset-pagination bounds for the audit & request-logs page-jump UI. The page
// cap protects against deep-offset scans and unbounded COUNT(*) on the
// partitioned request_logs table (the UI's default 30-day window already keeps
// the working set bounded).
const (
	defaultPageSize = 20
	maxPageSize     = 100
	maxPage         = 500
)

// parsePage reads the 1-based page query param, clamped to [1, maxPage].
func parsePage(c *gin.Context) int {
	n, err := strconv.Atoi(c.Query("page"))
	if err != nil || n < 1 {
		return 1
	}
	if n > maxPage {
		return maxPage
	}
	return n
}

// parsePageSize reads the page_size query param, clamped to [1, maxPageSize].
func parsePageSize(c *gin.Context) int {
	n, err := strconv.Atoi(c.Query("page_size"))
	if err != nil || n < 1 {
		return defaultPageSize
	}
	if n > maxPageSize {
		return maxPageSize
	}
	return n
}

// parseBoolQuery parses a "true"/"false"/"1"/"0" query param into a *bool.
// Returns nil when the param is absent or unparseable, so the filter is skipped.
func parseBoolQuery(c *gin.Context, key string) *bool {
	s := c.Query(key)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return nil
	}
	return &v
}

func exportUsageCSV(c *gin.Context, rows []store.UsageRow) {
	headers := []string{"id", "tenant", "group_name", "api_key_id", "provider", "model",
		"prompt_tokens", "completion_tokens", "cost", "created_at"}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			fmt.Sprintf("%d", r.ID), r.Tenant, r.GroupName, r.APIKeyID,
			r.Provider, r.Model,
			fmt.Sprintf("%d", r.PromptTokens), fmt.Sprintf("%d", r.CompletionTokens),
			fmt.Sprintf("%d", r.Cost),
			r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	writeCSV(c, "usage.csv", headers, out)
}
