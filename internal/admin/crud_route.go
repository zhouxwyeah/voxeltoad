package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// mountRouteCRUD wires route CRUD. Candidate providers must exist and be
// upstreams of the referenced model (ADR-0014: Route.Providers ⊆
// Model.Upstreams at write time → 400).
func mountRouteCRUD(g *gin.RouterGroup, repo *store.ConfigRepo, auth *rbac) {
	routes := g.Group("/routes", auth.auditMutation("route", resourceIDFrom))
	routes.POST("", func(c *gin.Context) {
		var rt config.Route
		if !bind(c, &rt) {
			return
		}
		if rt.ModelAlias == "" {
			badRequest(c, "route model_alias is required")
			return
		}
		switch rt.Strategy {
		case "priority", "weighted", "round_robin", "session_affinity":
			// ok
		default:
			badRequest(c, "route strategy must be one of priority, weighted, round_robin, session_affinity")
			return
		}
		known, err := providerSet(c.Request.Context(), repo)
		if err != nil {
			internalErr(c, err)
			return
		}
		for _, rp := range rt.Providers {
			if !known[rp.Name] {
				badRequest(c, "route references unknown provider "+rp.Name)
				return
			}
		}
		// Validate ModelAlias exists and every route provider is an upstream of
		// that model (ADR-0014: Route.Providers ⊆ Model.Upstreams at write time → 400).
		m, modelExists, err := repo.GetModel(c.Request.Context(), rt.ModelAlias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !modelExists {
			badRequest(c, "route references unknown model "+rt.ModelAlias)
			return
		}
		upstreamProviders := make(map[string]bool, len(m.Upstreams))
		for _, u := range m.Upstreams {
			upstreamProviders[u.Provider] = true
		}
		for _, rp := range rt.Providers {
			if !upstreamProviders[rp.Name] {
				badRequest(c, "route provider "+rp.Name+" is not an upstream of model "+rt.ModelAlias)
				return
			}
		}
		if err := repo.UpsertRoute(c.Request.Context(), rt); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, rt.ModelAlias)
		c.JSON(http.StatusCreated, rt)
	})
	routes.GET("", func(c *gin.Context) {
		cursor := c.Query("cursor")
		limit, _ := strconv.Atoi(c.Query("limit"))
		list, nextCursor, err := repo.ListRoutesPaged(c.Request.Context(), cursor, limit)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, nextCursor))
	})
	routes.PATCH("/:alias", func(c *gin.Context) {
		alias := c.Param("alias")
		var patch store.RoutePatch
		if !bind(c, &patch) {
			return
		}
		// Load current to validate the merged value (ADR-0030 pattern).
		current, ok, err := repo.GetRoute(c.Request.Context(), alias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.RouteNotFound)
			return
		}
		merged := current
		if patch.Strategy != nil {
			merged.Strategy = *patch.Strategy
		}
		if patch.Providers != nil {
			merged.Providers = *patch.Providers
		}
		switch merged.Strategy {
		case "priority", "weighted", "round_robin", "session_affinity":
			// ok
		default:
			badRequest(c, "route strategy must be one of priority, weighted, round_robin, session_affinity")
			return
		}
		known, err := providerSet(c.Request.Context(), repo)
		if err != nil {
			internalErr(c, err)
			return
		}
		for _, rp := range merged.Providers {
			if !known[rp.Name] {
				badRequest(c, "route references unknown provider "+rp.Name)
				return
			}
		}
		// ⊆ Model.Upstreams invariant (ADR-0014).
		m, modelExists, err := repo.GetModel(c.Request.Context(), alias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !modelExists {
			badRequest(c, "route references unknown model "+alias)
			return
		}
		upstreamProviders := make(map[string]bool, len(m.Upstreams))
		for _, u := range m.Upstreams {
			upstreamProviders[u.Provider] = true
		}
		for _, rp := range merged.Providers {
			if !upstreamProviders[rp.Name] {
				badRequest(c, "route provider "+rp.Name+" is not an upstream of model "+alias)
				return
			}
		}
		updated, ok, err := repo.PatchRoute(c.Request.Context(), alias, patch)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.RouteNotFound)
			return
		}
		setResourceID(c, alias)
		c.JSON(http.StatusOK, updated)
	})
	routes.DELETE("/:alias", func(c *gin.Context) {
		if err := repo.DeleteRoute(c.Request.Context(), c.Param("alias")); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, c.Param("alias"))
		c.Status(http.StatusNoContent)
	})
}
