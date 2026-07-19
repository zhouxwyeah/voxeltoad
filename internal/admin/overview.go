package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/store"
)

// OverviewPayload is the response body for GET /api/v1/overview.
type OverviewPayload struct {
	Nodes       NodeOverview            `json:"nodes"`
	RecentStats RecentStatsOverview     `json:"recent_stats"`
	TopTenants  []store.UsageSummaryRow `json:"top_tenants"`
	AgentStats  []AgentUsageRow         `json:"agent_stats"`
}

// AgentUsageRow is one row of the per-agent rollup on the overview page.
// Empty agent_type rows (requests whose User-Agent didn't match a known agent
// detector — see internal/proxy/agentdetect.go) are returned as-is; the UI
// renders them under the empty label.
type AgentUsageRow struct {
	AgentType        string `json:"agent_type"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	DurationMs       int64  `json:"duration_ms"`
	TTFTms           int64  `json:"ttft_ms"`
	ErrorCount       int64  `json:"error_count"`
}

type NodeOverview struct {
	Online int `json:"online"`
	Total  int `json:"total"`
}

type RecentStatsOverview struct {
	TotalRequests  int64 `json:"total_requests"`
	TotalErrors    int64 `json:"total_errors"`
	TotalBlocked   int64 `json:"total_blocked"`
	TotalTokensIn  int64 `json:"total_tokens_in"`
	TotalTokensOut int64 `json:"total_tokens_out"`
}

// overviewWindow is the default look-back for "recent" stats when no ?window= is given.
const overviewWindow = 5 * time.Minute

// parseOverviewWindow resolves the ?window= query param into a duration. Supports
// "5m" (default), "1h", "24h". Unknown values fall back to the default so a bad
// param never 400s the dashboard.
func parseOverviewWindow(s string) time.Duration {
	switch s {
	case "1h":
		return time.Hour
	case "24h":
		return 24 * time.Hour
	case "5m", "":
		return overviewWindow
	default:
		return overviewWindow
	}
}

// mountOverview wires the business dashboard overview endpoint. super-admin
// only — mounted under globalGrp (see server.go).
func mountOverview(g *gin.RouterGroup, db *store.DB) {
	g.GET("/overview", func(c *gin.Context) {
		ctx := c.Request.Context()
		var p OverviewPayload

		// Node health from heartbeat data.
		dpRepo := store.NewDataPlaneRepo(db)
		nodes, err := dpRepo.List(ctx)
		if err != nil {
			internalErr(c, err)
			return
		}
		p.Nodes.Total = len(nodes)
		for _, n := range nodes {
			if n.Status == "online" {
				p.Nodes.Online++
			}
		}

		// Recent stats from request_logs over a short look-back window
		// (?window=5m|1h|24h, default 5m). This is an instantaneous health
		// indicator — intentionally NOT affected by the new ?from/&to range
		// selector, which drives the TopTenants and AgentStats tables below.
		window := parseOverviewWindow(c.Query("window"))
		p.RecentStats = queryRecentStats(ctx, db, window)

		// Optional ?from/&to (RFC3339) drive the time-scoped tables. Absent
		// both → fall back to last 24h so a bare curl / old SDK still returns
		// sensible data instead of an unbounded scan.
		from, to, ok := parseTimeRange(c)
		if !ok {
			return
		}

		usageRepo := store.NewUsageQueryRepo(db, "") // global view
		p.TopTenants = queryTopTenants(ctx, usageRepo, from, to)
		p.AgentStats = queryAgentStats(ctx, db, from, to)

		c.JSON(http.StatusOK, p)
	})
}

func queryRecentStats(ctx context.Context, db *store.DB, window time.Duration) RecentStatsOverview {
	since := time.Now().UTC().Add(-window)
	var row struct {
		Total     int64 `gorm:"column:total"`
		Errors    int64 `gorm:"column:errors"`
		Blocked   int64 `gorm:"column:blocked"`
		TokensIn  int64 `gorm:"column:tokens_in"`
		TokensOut int64 `gorm:"column:tokens_out"`
	}
	db.WithContext(ctx).Raw(
		`SELECT coalesce(count(*), 0)   AS "total",
		        coalesce(sum(case when error_type != '' then 1 else 0 end), 0) AS errors,
		        coalesce(sum(case when blocked_by != '' then 1 else 0 end), 0)  AS blocked,
		        coalesce(sum(prompt_tokens), 0)     AS tokens_in,
		        coalesce(sum(completion_tokens), 0) AS tokens_out
		 FROM request_logs WHERE created_at >= ?`, since,
	).Scan(&row)
	return RecentStatsOverview{
		TotalRequests:  row.Total,
		TotalErrors:    row.Errors,
		TotalBlocked:   row.Blocked,
		TotalTokensIn:  row.TokensIn,
		TotalTokensOut: row.TokensOut,
	}
}

// queryTopTenants returns the top 5 tenants by token volume over the given
// time window. When both from and to are zero (no ?from/&to passed), it falls
// back to the last 24h so a bare curl / old SDK still gets sensible data.
func queryTopTenants(ctx context.Context, repo *store.UsageQueryRepo, from, to time.Time) []store.UsageSummaryRow {
	if from.IsZero() && to.IsZero() {
		to = time.Now().UTC().Add(24 * time.Hour)
	}
	rows, err := repo.Summary(ctx, from, to, "tenant")
	if err != nil {
		return nil
	}
	if len(rows) > 5 {
		rows = rows[:5]
	}
	return rows
}

// queryAgentStats returns a per-agent_type rollup over the given time window.
// When both from and to are zero, falls back to the last 24h. Rows are ordered
// by request_count DESC. SQL mirrors internal/desktopstore/query.go:441 so the
// desktop and admin overviews stay semantically aligned.
func queryAgentStats(ctx context.Context, db *store.DB, from, to time.Time) []AgentUsageRow {
	if from.IsZero() && to.IsZero() {
		from = time.Now().UTC().Add(-24 * time.Hour)
	}
	where := "1=1"
	var args []any
	if !from.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, from)
	}
	if !to.IsZero() {
		where += " AND created_at < ?"
		args = append(args, to)
	}
	q := `SELECT agent_type,
	             COUNT(*)                                                        AS request_count,
	             COALESCE(SUM(prompt_tokens), 0)                                 AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0)                             AS completion_tokens,
	             COALESCE(SUM(total_tokens), 0)                                  AS total_tokens,
	             COALESCE(SUM(duration_ms), 0)                                   AS duration_ms,
	             COALESCE(SUM(ttft_ms), 0)                                       AS ttft_ms,
	             COALESCE(SUM(CASE WHEN error_type <> '' THEN 1 ELSE 0 END), 0)  AS error_count
	      FROM request_logs
	      WHERE ` + where + `
	      GROUP BY agent_type
	      ORDER BY request_count DESC`
	var rows []AgentUsageRow
	if err := db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil
	}
	if rows == nil {
		rows = []AgentUsageRow{}
	}
	return rows
}
