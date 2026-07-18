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

		// Recent stats from request_logs (last window). The window is now
		// client-controllable via ?window=5m|1h|24h (default 5m).
		window := parseOverviewWindow(c.Query("window"))
		p.RecentStats = queryRecentStats(ctx, db, window)

		// Top 5 tenants by token volume in the last 24h.
		usageRepo := store.NewUsageQueryRepo(db, "") // global view
		p.TopTenants = queryTopTenants(ctx, usageRepo)

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

func queryTopTenants(ctx context.Context, repo *store.UsageQueryRepo) []store.UsageSummaryRow {
	rows, err := repo.Summary(ctx, time.Time{}, time.Now().UTC().Add(24*time.Hour), "tenant")
	if err != nil {
		return nil
	}
	if len(rows) > 5 {
		rows = rows[:5]
	}
	return rows
}
