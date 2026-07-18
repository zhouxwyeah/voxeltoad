package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/authz"
	"voxeltoad/internal/store"
)

// mountPermissions exposes the permission catalog as GET /api/v1/permissions.
// The catalog is the single source of truth (authz.AllPermissions) and is
// consumed by the frontend role management page to render the permission matrix.
func mountPermissions(g *gin.RouterGroup, catalog []authz.Entry) {
	g.GET("/permissions", func(c *gin.Context) {
		type item struct {
			Perm  string `json:"perm"`
			Scope string `json:"scope"`
			Label string `json:"label"`
		}
		out := make([]item, len(catalog))
		for i, e := range catalog {
			out[i] = item{Perm: string(e.Perm), Scope: string(e.Scope), Label: e.Label}
		}
		c.JSON(http.StatusOK, gin.H{"data": out})
	})
}

// mountRoles wires the roles CRUD endpoints under the given group
// (expected to be already gated by requirePermission(PermRoleRead/PermRoleWrite)).
// Every mutation is audited with resource_type "role".
func mountRoles(g *gin.RouterGroup, roleRepo *store.RoleRepo, db *store.DB, auth *rbac) {
	rolesGrp := g.Group("/roles", auth.auditMutation("role", resourceIDFrom))

	// GET /api/v1/roles — list all roles with their permission sets.
	rolesGrp.GET("", func(c *gin.Context) {
		roles, err := roleRepo.List(c.Request.Context())
		if err != nil {
			internalErr(c, err)
			return
		}
		type roleWithPerms struct {
			store.Role
			Permissions []string `json:"permissions"`
		}
		out := make([]roleWithPerms, 0, len(roles))
		for _, r := range roles {
			perms, err := roleRepo.LoadPermissions(c.Request.Context(), r.ID)
			if err != nil {
				internalErr(c, err)
				return
			}
			if perms == nil {
				perms = []string{}
			}
			out = append(out, roleWithPerms{Role: r, Permissions: perms})
		}
		c.JSON(http.StatusOK, gin.H{"data": out})
	})

	// POST /api/v1/roles — create a new custom role.
	rolesGrp.POST("", func(c *gin.Context) {
		var body struct {
			Name        string   `json:"name"`
			ScopeKind   string   `json:"scope_kind"`
			Description string   `json:"description"`
			Permissions []string `json:"permissions"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			appErr(c, apperr.InvalidBody)
			return
		}
		if body.Name == "" {
			badRequest(c, "name is required")
			return
		}
		if body.ScopeKind != string(authz.ScopeGlobal) && body.ScopeKind != string(authz.ScopeTenant) {
			badRequest(c, "scope_kind must be global or tenant")
			return
		}
		// Validate every permission against the catalog.
		validPerms := make(map[string]bool)
		for _, e := range authz.AllPermissions() {
			validPerms[string(e.Perm)] = true
		}
		validPerms[string(authz.Wildcard)] = true
		for _, p := range body.Permissions {
			if !validPerms[p] {
				badRequest(c, "unknown permission: "+p)
				return
			}
		}

		_, ok, err := roleRepo.GetByName(c.Request.Context(), body.Name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if ok {
			badRequest(c, "role name already exists")
			return
		}

		role := &store.Role{
			Name:        body.Name,
			ScopeKind:   body.ScopeKind,
			Description: body.Description,
		}
		if err := roleRepo.Create(c.Request.Context(), role, body.Permissions); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, strconv.FormatInt(role.ID, 10))
		perms, _ := roleRepo.LoadPermissions(c.Request.Context(), role.ID)
		if perms == nil {
			perms = []string{}
		}
		c.JSON(http.StatusCreated, gin.H{
			"id":          role.ID,
			"name":        role.Name,
			"scope_kind":  role.ScopeKind,
			"is_builtin":  false,
			"description": role.Description,
			"permissions": perms,
		})
	})

	// PATCH /api/v1/roles/:id — update description and/or permissions.
	rolesGrp.PATCH("/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			badRequest(c, "invalid role id")
			return
		}
		_, ok, err := roleRepo.GetByID(c.Request.Context(), id)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.RoleNotFound)
			return
		}

		var body struct {
			Description *string  `json:"description"`
			Permissions []string `json:"permissions"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			appErr(c, apperr.InvalidBody)
			return
		}

		// Built-in roles: name and scope_kind are immutable (the request body
		// cannot change them); only permissions and description are updatable.

		if body.Description != nil {
			if err := roleRepo.UpdateDescription(c.Request.Context(), id, *body.Description); err != nil {
				internalErr(c, err)
				return
			}
		}
		if body.Permissions != nil {
			// Validate every permission against the catalog.
			validPerms := make(map[string]bool)
			for _, e := range authz.AllPermissions() {
				validPerms[string(e.Perm)] = true
			}
			validPerms[string(authz.Wildcard)] = true
			for _, p := range body.Permissions {
				if !validPerms[p] {
					badRequest(c, "unknown permission: "+p)
					return
				}
			}
			if err := roleRepo.UpdatePermissions(c.Request.Context(), id, body.Permissions); err != nil {
				internalErr(c, err)
				return
			}
		}

		// Re-read to return updated state.
		setResourceID(c, strconv.FormatInt(id, 10))
		updated, _, _ := roleRepo.GetByID(c.Request.Context(), id)
		perms, _ := roleRepo.LoadPermissions(c.Request.Context(), id)
		if perms == nil {
			perms = []string{}
		}
		c.JSON(http.StatusOK, gin.H{
			"id":          updated.ID,
			"name":        updated.Name,
			"scope_kind":  updated.ScopeKind,
			"is_builtin":  updated.IsBuiltin,
			"description": updated.Description,
			"permissions": perms,
		})
	})

	// DELETE /api/v1/roles/:id — delete a custom role.
	rolesGrp.DELETE("/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			badRequest(c, "invalid role id")
			return
		}
		role, ok, err := roleRepo.GetByID(c.Request.Context(), id)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.RoleNotFound)
			return
		}
		if role.IsBuiltin {
			badRequest(c, "built-in role cannot be deleted")
			return
		}
		if err := roleRepo.Delete(c.Request.Context(), id); err != nil {
			badRequest(c, err.Error()) // wraps RoleInUse message
			return
		}
		setResourceID(c, strconv.FormatInt(id, 10))
		c.Status(http.StatusNoContent)
	})
}
