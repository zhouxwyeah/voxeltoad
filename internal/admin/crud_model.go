package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// mountModelCRUD wires model CRUD. Read (GET) is open to any authenticated
// operator (both super-admin and tenant-admin, since models are global shared
// config and the API-key form needs model aliases for the allowed_models
// selector). Writes (POST/PATCH/DELETE) remain super-admin only.
//
// Upstream providers must exist (validated at write time → 400). Updating a
// model's upstreams must not orphan an existing route referencing it
// (Route.Providers ⊆ Model.Upstreams, ADR-0014).
func mountModelCRUD(readGroup *gin.RouterGroup, writeGroup *gin.RouterGroup, repo *store.ConfigRepo, auth *rbac) {
	// GET — any authenticated operator (no role gate beyond authn)
	readGroup.GET("/models", func(c *gin.Context) {
		list, next, err := repo.ListModelsPaged(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, next))
	})

	models := writeGroup.Group("/models", auth.auditMutation("model", resourceIDFrom))
	models.POST("", func(c *gin.Context) {
		var m config.Model
		if !bind(c, &m) {
			return
		}
		if m.Alias == "" {
			badRequest(c, "model alias is required")
			return
		}
		known, err := providerSet(c.Request.Context(), repo)
		if err != nil {
			internalErr(c, err)
			return
		}
		for _, u := range m.Upstreams {
			if !known[u.Provider] {
				badRequest(c, "model upstream references unknown provider "+u.Provider)
				return
			}
		}
		// Reverse check: updating this model's upstreams must not orphan any
		// existing route that references it (Route.Providers ⊆ Model.Upstreams,
		// strictly enforced at write time → 400).
		existingRoute, routeExists, err := repo.GetRoute(c.Request.Context(), m.Alias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if routeExists {
			newProviders := make(map[string]bool, len(m.Upstreams))
			for _, u := range m.Upstreams {
				newProviders[u.Provider] = true
			}
			for _, rp := range existingRoute.Providers {
				if !newProviders[rp.Name] {
					badRequest(c, "updating model would orphan route "+m.Alias+
						": provider "+rp.Name+" is in the route but not in the new upstreams")
					return
				}
			}
		}
		if err := repo.UpsertModel(c.Request.Context(), m); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, m.Alias)
		c.JSON(http.StatusCreated, m)
	})
	models.PATCH("/:alias", func(c *gin.Context) {
		alias := c.Param("alias")
		var patch store.ModelPatch
		if !bind(c, &patch) {
			return
		}
		// Load current to validate the merged value (ADR-0030 pattern).
		current, ok, err := repo.GetModel(c.Request.Context(), alias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ModelNotFound)
			return
		}
		merged := current
		if patch.Description != nil {
			merged.Description = *patch.Description
		}
		if patch.ContextLength != nil {
			merged.ContextLength = *patch.ContextLength
		}
		if patch.Capabilities != nil {
			merged.Capabilities = *patch.Capabilities
		}
		if patch.Tags != nil {
			merged.Tags = *patch.Tags
		}
		if patch.Upstreams != nil {
			merged.Upstreams = *patch.Upstreams
		}
		known, err := providerSet(c.Request.Context(), repo)
		if err != nil {
			internalErr(c, err)
			return
		}
		for _, u := range merged.Upstreams {
			if !known[u.Provider] {
				badRequest(c, "model upstream references unknown provider "+u.Provider)
				return
			}
		}
		// Reverse check: merged upstreams must not orphan any existing route
		// (Route.Providers ⊆ Model.Upstreams, ADR-0014).
		existingRoute, routeExists, err := repo.GetRoute(c.Request.Context(), alias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if routeExists {
			newProviders := make(map[string]bool, len(merged.Upstreams))
			for _, u := range merged.Upstreams {
				newProviders[u.Provider] = true
			}
			for _, rp := range existingRoute.Providers {
				if !newProviders[rp.Name] {
					badRequest(c, "updating model would orphan route "+alias+
						": provider "+rp.Name+" is in the route but not in the new upstreams")
					return
				}
			}
		}
		updated, ok, err := repo.PatchModel(c.Request.Context(), alias, patch)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ModelNotFound)
			return
		}
		setResourceID(c, alias)
		c.JSON(http.StatusOK, updated)
	})
	models.DELETE("/:alias", func(c *gin.Context) {
		alias := c.Param("alias")
		refRoutes, err := repo.ModelReferencedBy(c.Request.Context(), alias)
		if err != nil {
			internalErr(c, err)
			return
		}
		if len(refRoutes) > 0 {
			appErrMsg(c, apperr.ModelDeleteFailed,
				"referenced by route(s) "+strings.Join(refRoutes, ", ")+"; delete or repoint them first")
			return
		}
		if err := repo.DeleteModel(c.Request.Context(), alias); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, alias)
		c.Status(http.StatusNoContent)
	})
}
