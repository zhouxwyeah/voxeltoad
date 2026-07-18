package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// mountConfigHistory wires config version history, diff, rollback, and dry-run
// preview endpoints. All require super-admin (mounted under globalGrp).
func mountConfigHistory(g *gin.RouterGroup, repo *store.ConfigRepo, snap *store.ConfigSnapshotRepo) {
	if snap == nil {
		return
	}
	h := g.Group("/config")
	// GET /api/v1/config/history — list snapshots (keyset cursor pagination)
	h.GET("/history", func(c *gin.Context) {
		cursor := c.Query("cursor")
		limit := 0
		if l := c.Query("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil {
				limit = v
			}
		}
		rows, next, err := snap.ListSnapshots(c.Request.Context(), cursor, limit)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(rows, next))
	})

	// GET /api/v1/config/history/diff?from=X&to=Y — structured diff.
	// MUST be registered before /history/:version so Gin's radix tree routes
	// the literal "diff" segment before the parameterised ":version".
	h.GET("/history/diff", func(c *gin.Context) {
		from, err := strconv.ParseInt(c.Query("from"), 10, 64)
		if err != nil || from <= 0 {
			badRequest(c, "from parameter is required (positive integer)")
			return
		}
		to, err := strconv.ParseInt(c.Query("to"), 10, 64)
		if err != nil || to <= 0 {
			badRequest(c, "to parameter is required (positive integer)")
			return
		}
		diff, err := snap.Diff(c.Request.Context(), from, to)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, diff)
	})

	// GET /api/v1/config/history/:version — full snapshot at a version.
	// Registered after /history/diff so "diff" routes first.
	h.GET("/history/:version", func(c *gin.Context) {
		v, err := strconv.ParseInt(c.Param("version"), 10, 64)
		if err != nil {
			badRequest(c, "version must be an integer")
			return
		}
		d, err := snap.Get(c.Request.Context(), v)
		if err != nil {
			internalErr(c, err)
			return
		}
		if d == nil {
			c.JSON(http.StatusNotFound, errBody("version_not_found", "version not found"))
			return
		}
		c.JSON(http.StatusOK, d)
	})

	// POST /api/v1/config/rollback — rollback to a specific version.
	// NOTE: This handler does NOT emit an audit_logs row (Phase 3 enhancement).
	// The rollback is implicitly audited via config_snapshots — each rollback
	// produces a new snapshot version with bumped config_generation.
	h.POST("/rollback", func(c *gin.Context) {
		var req struct {
			Version int64 `json:"version"`
		}
		if !bind(c, &req) {
			return
		}
		if req.Version <= 0 {
			badRequest(c, "version is required (positive integer)")
			return
		}
		if err := snap.Rollback(c.Request.Context(), req.Version); err != nil {
			internalErr(c, err)
			return
		}
		c.Status(http.StatusOK)
	})

	// POST /api/v1/config/preview — dry-run: validate + diff against current
	h.POST("/preview", func(c *gin.Context) {
		var preview config.Dynamic
		if !bind(c, &preview) {
			return
		}
		// Validate the config syntactically (basic checks).
		validationErrs := validateDynamicConfig(&preview)
		if len(validationErrs) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"errors": validationErrs})
			return
		}
		// Build diff against current snapshot.
		curr, err := repo.Snapshot(c.Request.Context())
		if err != nil {
			internalErr(c, err)
			return
		}
		diff := computeInlineDiff(curr, &preview)
		// Compute impact: which resources would be added/deleted/changed.
		impact := computeImpact(curr, &preview)
		c.JSON(http.StatusOK, gin.H{
			"valid":    true,
			"diff":     diff,
			"impact":   impact,
			"warnings": []string{}, // future: check for unresolved refs etc.
		})
	})
}

// validateDynamicConfig performs basic validation on a dry-run config payload.
func validateDynamicConfig(d *config.Dynamic) []string {
	var errs []string
	for _, p := range d.Providers {
		if p.Name == "" {
			errs = append(errs, "provider name is required")
		}
	}
	for _, m := range d.Models {
		if m.Alias == "" {
			errs = append(errs, "model alias is required")
		}
	}
	for _, rt := range d.Routes {
		if rt.ModelAlias == "" {
			errs = append(errs, "route model_alias is required")
		}
	}
	return errs
}

// computeInlineDiff computes a simple diff between two Dynamic configs.
func computeInlineDiff(from, to *config.Dynamic) map[string]interface{} {
	fp := setBySlice(from.Providers, func(p config.Provider) string { return p.Name })
	tp := setBySlice(to.Providers, func(p config.Provider) string { return p.Name })
	fm := setBySlice(from.Models, func(m config.Model) string { return m.Alias })
	tm := setBySlice(to.Models, func(m config.Model) string { return m.Alias })
	fr := setBySlice(from.Routes, func(rt config.Route) string { return rt.ModelAlias })
	tr := setBySlice(to.Routes, func(rt config.Route) string { return rt.ModelAlias })
	fpl := setBySlice(from.Plugins, func(pc config.PluginConfig) string { return pc.Name + "/" + pc.Scope })
	tpl := setBySlice(to.Plugins, func(pc config.PluginConfig) string { return pc.Name + "/" + pc.Scope })

	diff := map[string]interface{}{}
	diff["added_providers"] = diffKeys(tp, fp)
	diff["removed_providers"] = diffKeys(fp, tp)
	diff["added_models"] = diffKeys(tm, fm)
	diff["removed_models"] = diffKeys(fm, tm)
	diff["added_routes"] = diffKeys(tr, fr)
	diff["removed_routes"] = diffKeys(fr, tr)
	diff["added_plugins"] = diffKeys(tpl, fpl)
	diff["removed_plugins"] = diffKeys(fpl, tpl)
	return diff
}

func setBySlice[T any, K comparable](slice []T, keyFn func(T) K) map[K]struct{} {
	m := make(map[K]struct{}, len(slice))
	for _, v := range slice {
		m[keyFn(v)] = struct{}{}
	}
	return m
}

func diffKeys[K comparable](from, to map[K]struct{}) []K {
	var diff []K
	for k := range from {
		if _, ok := to[k]; !ok {
			diff = append(diff, k)
		}
	}
	return diff
}

// ImpactSummary describes what a preview change will affect.
// ChangedResources is reserved for future per-resource diff (currently 0).
type ImpactSummary struct {
	NewResources     int `json:"new_resources"`
	DeletedResources int `json:"deleted_resources"`
	ChangedResources int `json:"changed_resources"` // reserved: not yet implemented
}

func computeImpact(from, to *config.Dynamic) *ImpactSummary {
	s := &ImpactSummary{}
	// New = in "to" but not in "from"
	fp := setBySlice(from.Providers, func(p config.Provider) string { return p.Name })
	tp := setBySlice(to.Providers, func(p config.Provider) string { return p.Name })
	s.NewResources += len(diffKeys(tp, fp))
	s.DeletedResources += len(diffKeys(fp, tp))

	fm := setBySlice(from.Models, func(m config.Model) string { return m.Alias })
	tm := setBySlice(to.Models, func(m config.Model) string { return m.Alias })
	s.NewResources += len(diffKeys(tm, fm))
	s.DeletedResources += len(diffKeys(fm, tm))

	fr := setBySlice(from.Routes, func(rt config.Route) string { return rt.ModelAlias })
	tr := setBySlice(to.Routes, func(rt config.Route) string { return rt.ModelAlias })
	s.NewResources += len(diffKeys(tr, fr))
	s.DeletedResources += len(diffKeys(fr, tr))

	fpl := setBySlice(from.Plugins, func(pc config.PluginConfig) string { return pc.Name + "/" + pc.Scope })
	tpl := setBySlice(to.Plugins, func(pc config.PluginConfig) string { return pc.Name + "/" + pc.Scope })
	s.NewResources += len(diffKeys(tpl, fpl))
	s.DeletedResources += len(diffKeys(fpl, tpl))

	return s
}
