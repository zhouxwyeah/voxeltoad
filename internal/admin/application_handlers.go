package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/authz"
	"voxeltoad/internal/store"
)

// mountApplications wires tenant-admin Application CRUD (ADR-0051). Applications
// are tenant-scoped governance identities; the owning Group is required at
// creation, and disablement rejects all bound keys at the data-plane auth
// boundary (KeyRepo.LookupByHash), not via cascading writes.
func mountApplications(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	apps := g.Group("/applications", auth.auditMutation("application", resourceIDFrom))

	apps.POST("", auth.requirePermission(authz.PermApplicationWrite), func(c *gin.Context) {
		op := operatorFrom(c)
		var body struct {
			Name       string `json:"name"`
			OwnerGroup string `json:"owner_group"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Name == "" {
			appErr(c, apperr.ApplicationNameRequired)
			return
		}
		if body.OwnerGroup == "" {
			appErr(c, apperr.ApplicationOwnerRequired)
			return
		}
		repo := store.NewApplicationRepo(db, *op.TenantID)
		_, err := repo.Create(c.Request.Context(), body.Name, body.OwnerGroup)
		if err != nil {
			if isConstraintViolation(err) {
				badRequest(c, "application create rejected: "+err.Error())
				return
			}
			internalErr(c, err)
			return
		}
		// Read back the full record (with JOIN-resolved owner_group_name) so
		// the response matches the Application OpenAPI schema.
		app, ok, err := repo.Get(c.Request.Context(), body.Name)
		if err != nil || !ok {
			internalErr(c, fmt.Errorf("application created but not found: %w", err))
			return
		}
		setResourceID(c, body.Name)
		c.JSON(http.StatusCreated, app)
	})

	apps.GET("", auth.requirePermission(authz.PermApplicationRead), func(c *gin.Context) {
		op := operatorFrom(c)
		repo := store.NewApplicationRepo(db, *op.TenantID)
		list, next, err := repo.List(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, next))
	})

	apps.PATCH("/:name", auth.requirePermission(authz.PermApplicationWrite), func(c *gin.Context) {
		op := operatorFrom(c)
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Enabled == nil {
			badRequest(c, "enabled is required")
			return
		}
		name := c.Param("name")
		repo := store.NewApplicationRepo(db, *op.TenantID)
		ok, err := repo.SetEnabled(c.Request.Context(), name, *body.Enabled)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ApplicationNotFound)
			return
		}
		app, found, err := repo.Get(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !found {
			appErr(c, apperr.ApplicationNotFound)
			return
		}
		setResourceID(c, name)
		c.JSON(http.StatusOK, app)
	})

	apps.DELETE("/:name", auth.requirePermission(authz.PermApplicationWrite), func(c *gin.Context) {
		op := operatorFrom(c)
		name := c.Param("name")
		repo := store.NewApplicationRepo(db, *op.TenantID)
		refs, err := repo.ApplicationReferencedByAPIKeys(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if len(refs) > 0 {
			appErrMsg(c, apperr.ApplicationReferenced,
				"api_key(s) "+strings.Join(refs, ", ")+" retain this application; disable the application instead")
			return
		}
		ok, err := repo.Delete(c.Request.Context(), name)
		if err != nil {
			if isConstraintViolation(err) {
				appErr(c, apperr.ApplicationReferenced)
				return
			}
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ApplicationNotFound)
			return
		}
		setResourceID(c, name)
		c.Status(http.StatusNoContent)
	})
}
