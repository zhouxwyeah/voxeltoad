package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/store"
)

// mountMe wires the GET /api/v1/me endpoint, available to any authenticated
// operator with a valid role (Phase-2 RBAC: custom roles are supported).
// Returns the operator's id, email, role, role_id, scope_kind, permissions,
// tenant_id, and tenant_name. The front-end uses permissions and scope_kind
// for authorization; role is kept as a legacy display field.
func mountMe(g *gin.RouterGroup, db *store.DB) {
	g.GET("/me", func(c *gin.Context) {
		op := operatorFrom(c)

		// A valid operator must have a role_id (Phase-2 RBAC). Legacy operators
		// without a populated RoleID are rejected — they need re-migration.
		if op.RoleID == 0 {
			appErr(c, apperr.UnknownOperatorRole)
			return
		}

		// Build the permissions array from the loaded set.
		perms := make([]string, 0, len(op.Permissions))
		for p := range op.Permissions {
			perms = append(perms, p)
		}

		out := gin.H{
			"id":          op.ID,
			"email":       op.Email,
			"role":        string(op.Role),
			"role_id":     op.RoleID,
			"scope_kind":  op.ScopeKind,
			"permissions": perms,
		}

		if op.TenantID != nil {
			out["tenant_id"] = *op.TenantID
			name, err := store.TenantName(c.Request.Context(), db, *op.TenantID)
			if err != nil {
				internalErr(c, err)
				return
			}
			if name != "" {
				out["tenant_name"] = name
			} else {
				out["tenant_name"] = nil
			}
		} else {
			out["tenant_id"] = nil
			out["tenant_name"] = nil
		}
		c.JSON(http.StatusOK, out)
	})
}
