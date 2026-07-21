package admin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/store"
)

// mountRequestLogs wires the read-only data-plane request-audit feed for BOTH
// roles (ADR-0021 §7, following the ADR-0019 read pattern): super-admin sees
// all tenants; a tenant-admin is scoped to its own tenant. Scope is resolved
// from the operator, never the request, so a tenant-admin cannot read another
// tenant's request log. Reads are not audited (ADR-0017 §5).
func mountRequestLogs(g *gin.RouterGroup, db *store.DB) {
	// Session list (aggregated): MUST be registered before the
	// /request-logs/sessions/:session_id route below, otherwise the path-param
	// route would shadow it. Returns per-session totals (tokens, duration, cost,
	// agent type) grouped over request_logs + usage_records.
	g.GET("/request-logs/sessions", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}
		page := parsePage(c)
		pageSize := parsePageSize(c)

		repo := store.NewRequestLogQueryRepo(db, tenant)
		sessions, total, err := repo.ListSessions(c.Request.Context(), store.SessionListFilter{
			AgentType: c.Query("agent_type"),
			From:      from,
			To:        to,
		}, page, pageSize)
		if err != nil {
			internalErr(c, err)
			return
		}

		// Batch-merge cost from usage_records (avoids N+1 SummaryBySession).
		if len(sessions) > 0 {
			ids := make([]string, len(sessions))
			for i, s := range sessions {
				ids[i] = s.SessionID
			}
			usageRepo := store.NewUsageQueryRepo(db, tenant)
			costs, err := usageRepo.CostBySessions(c.Request.Context(), ids)
			if err != nil {
				internalErr(c, err)
				return
			}
			for i := range sessions {
				sessions[i].Cost = costs[sessions[i].SessionID]
			}
		}

		c.JSON(http.StatusOK, offsetListEnvelope(sessions, total, page, pageSize))
	})

	g.GET("/request-logs", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db) // same operator→tenant resolution as usage/audit
		if !ok {
			return
		}
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}
		repo := store.NewRequestLogQueryRepo(db, tenant)
		filter := store.RequestLogFilter{
			Provider:        c.Query("provider"),
			ModelRequested:  c.Query("model_requested"),
			ErrorType:       c.Query("error_type"),
			BlockedBy:       c.Query("blocked_by"),
			Tenant:          c.Query("tenant"),
			GroupName:       c.Query("group_name"),
			APIKeyID:        c.Query("api_key_id"),
			Stream:          parseBoolQuery(c, "stream"),
			Fallback:        parseBoolQuery(c, "fallback"),
			AgentType:       c.Query("agent_type"),
			IngressProtocol: c.Query("ingress_protocol"),
			SessionID:       c.Query("session_id"),
			RequestID:       c.Query("request_id"),
			From:            from,
			To:              to,
		}
		if c.Query("format") == "csv" {
			rows, _, err := repo.List(c.Request.Context(), filter, "", 2000)
			if err != nil {
				internalErr(c, err)
				return
			}
			exportRequestLogsCSV(c, rows)
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

	// Session trace: returns the chronological request timeline for a session
	// plus the session's aggregated cost summary (from usage_records). Enables
	// the "session chain" analysis view. Tenant scope applies identically.
	g.GET("/request-logs/sessions/:session_id", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		sessionID := c.Param("session_id")
		if sessionID == "" {
			badRequest(c, "session_id path parameter is required")
			return
		}

		logRepo := store.NewRequestLogQueryRepo(db, tenant)
		requests, err := logRepo.ListBySession(c.Request.Context(), sessionID, parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		usageRepo := store.NewUsageQueryRepo(db, tenant)
		summary, err := usageRepo.SummaryBySession(c.Request.Context(), sessionID)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"session_id":   sessionID,
			"requests":     requests,
			"cost_summary": summary,
		})
	})
}

func exportRequestLogsCSV(c *gin.Context, rows []store.RequestLogRow) {
	headers := []string{"id", "tenant", "group_name", "api_key_id", "provider",
		"model_requested", "model_resolved", "stream",
		"prompt_tokens", "completion_tokens", "total_tokens",
		"ttft_ms", "duration_ms", "error_type", "blocked_by", "fallback",
		"request_id", "session_id", "trace_id", "session_source", "agent_type",
		"cache_hit", "cache_tier", "cache_source", "cached_prompt_tokens",
		"ingress_protocol", "created_at"}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			fmt.Sprintf("%d", r.ID),
			r.Tenant, r.GroupName, r.APIKeyID,
			r.Provider, r.ModelRequested, r.ModelResolved,
			fmt.Sprintf("%t", r.Stream),
			fmt.Sprintf("%d", r.PromptTokens), fmt.Sprintf("%d", r.CompletionTokens), fmt.Sprintf("%d", r.TotalTokens),
			fmt.Sprintf("%d", r.TTFTms), fmt.Sprintf("%d", r.Durationms),
			r.ErrorType, r.BlockedBy,
			fmt.Sprintf("%t", r.Fallback),
			r.RequestID, r.SessionID, r.TraceID, r.SessionSource, r.AgentType,
			fmt.Sprintf("%t", r.CacheHit), r.CacheTier, r.CacheSource,
			fmt.Sprintf("%d", r.CachedPromptTokens),
			r.IngressProtocol,
			r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	writeCSV(c, "request_logs.csv", headers, out)
}
