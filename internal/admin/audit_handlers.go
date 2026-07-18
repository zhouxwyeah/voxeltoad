package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/store"
)

// mountAudit wires the read-only audit feed for BOTH roles (ADR-0019):
// super-admin sees all rows; a tenant-admin sees only rows attributed to its
// tenant — including super-admin actions on it (the tenant column is the
// affected tenant). Scope is resolved from the operator, never the request.
// Reads are not audited.
func mountAudit(g *gin.RouterGroup, db *store.DB) {
	g.GET("/audit", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db) // same operator→tenant resolution
		if !ok {
			return
		}
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}
		repo := store.NewAuditQueryRepo(db, tenant)
		var opID *int64
		if raw := c.Query("operator_id"); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
				opID = &v
			}
		}
		filter := store.AuditFilter{
			ResourceType: c.Query("resource_type"),
			ResourceID:   c.Query("resource_id"),
			Action:       c.Query("action"),
			OperatorID:   opID,
			From:         from,
			To:           to,
		}
		if c.Query("format") == "csv" {
			rows, _, err := repo.List(c.Request.Context(), filter, "", 2000)
			if err != nil {
				internalErr(c, err)
				return
			}
			exportAuditCSV(c, rows)
			return
		}
		page := parsePage(c)
		pageSize := parsePageSize(c)
		rows, total, err := repo.ListPage(c.Request.Context(), filter, page, pageSize)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, offsetListEnvelope(rows, total, page, pageSize))
	})
}

func exportAuditCSV(c *gin.Context, rows []store.AuditRow) {
	headers := []string{"id", "operator_id", "tenant", "action",
		"resource_type", "resource_id", "after_json", "created_at"}
	out := make([][]string, len(rows))
	for i, r := range rows {
		opStr := ""
		if r.OperatorID != nil {
			opStr = fmt.Sprintf("%d", *r.OperatorID)
		}
		tenantStr := ""
		if r.Tenant != nil {
			tenantStr = *r.Tenant
		}
		afterStr := "null"
		if len(r.After) > 0 {
			afterStr = string(r.After)
			if !json.Valid(r.After) {
				afterStr = fmt.Sprintf("%q", string(r.After)) // not valid JSON → quote
			}
		}
		out[i] = []string{
			fmt.Sprintf("%d", r.ID), opStr, tenantStr,
			r.Action, r.ResourceType, r.ResourceID,
			afterStr,
			r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	writeCSV(c, "audit_logs.csv", headers, out)
}
