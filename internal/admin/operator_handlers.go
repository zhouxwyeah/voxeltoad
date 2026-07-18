package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/authz"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// mountOperators wires super-admin operator management (ADR-0017 §4 extends
// bootstrap: this is how additional operators — especially tenant-admins — are
// created without hand-inserting DB rows, the UI-critical gap). super-admin
// only; every mutation is audited as resource_type "operator".
func mountOperators(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	repo := store.NewOperatorRepo(db)
	sessions := store.NewSessionRepo(db)

	ops := g.Group("/operators", auth.auditMutation("operator", resourceIDFrom))

	ops.POST("", func(c *gin.Context) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Role     string `json:"role"`
			RoleID   int64  `json:"role_id"` // Phase-2: preferred over role text
			TenantID *int64 `json:"tenant_id"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Email == "" || body.Password == "" {
			badRequest(c, "email and password are required")
			return
		}

		// Resolve role: role_id takes precedence over role text (Phase-2 RBAC).
		roleRepo := store.NewRoleRepo(db)
		var role operator.Role
		if body.RoleID > 0 {
			roleObj, ok, err := roleRepo.GetByID(c.Request.Context(), body.RoleID)
			if err != nil {
				internalErr(c, err)
				return
			}
			if !ok {
				badRequest(c, "role not found")
				return
			}
			role = operator.Role(roleObj.Name)
			// Validate scope_kind ⇔ tenant_id consistency.
			if roleObj.ScopeKind == string(authz.ScopeGlobal) && body.TenantID != nil {
				badRequest(c, "global role must not have a tenant_id")
				return
			}
			if roleObj.ScopeKind == string(authz.ScopeTenant) && body.TenantID == nil {
				badRequest(c, "tenant role requires a tenant_id")
				return
			}
		} else if body.Role != "" {
			// Legacy: resolve by role text string.
			role = operator.Role(body.Role)
			switch role {
			case operator.RoleSuperAdmin:
				if body.TenantID != nil {
					badRequest(c, "super-admin must not have a tenant_id")
					return
				}
			case operator.RoleTenantAdmin:
				if body.TenantID == nil {
					badRequest(c, "tenant-admin requires a tenant_id")
					return
				}
			default:
				badRequest(c, "role must be super-admin or tenant-admin (or use role_id for custom roles)")
				return
			}
		} else {
			badRequest(c, "role or role_id is required")
			return
		}

		hash, err := operator.HashPassword(body.Password)
		if err != nil {
			internalErr(c, err)
			return
		}
		op, err := repo.Create(c.Request.Context(), body.Email, hash, role, body.TenantID)
		if err != nil {
			// Unique-violation (duplicate email) or FK/CHECK → client error.
			if isConstraintViolation(err) {
				badRequest(c, "operator create rejected: "+err.Error())
				return
			}
			internalErr(c, err)
			return
		}
		setResourceID(c, body.Email)
		c.JSON(http.StatusCreated, store.OperatorInfo{
			ID: op.ID, Email: op.Email, Role: string(op.Role), TenantID: op.TenantID,
		})
	})

	ops.GET("", func(c *gin.Context) {
		list, next, err := repo.List(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, next))
	})

	ops.DELETE("/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			badRequest(c, "invalid operator id")
			return
		}

		// Lockout protection: never delete the last super-admin, or the platform
		// becomes unadministrable.
		target, ok, err := operatorByID(c.Request.Context(), db, id)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.OperatorNotFound)
			return
		}
		if target.Role == operator.RoleSuperAdmin {
			n, err := repo.CountByRole(c.Request.Context(), operator.RoleSuperAdmin)
			if err != nil {
				internalErr(c, err)
				return
			}
			if n <= 1 {
				appErr(c, apperr.LastSuperAdmin)
				return
			}
		}

		// Revoke sessions first (belt-and-suspenders; the FK also cascades), so
		// a deleted operator's token stops authenticating immediately.
		if err := sessions.DeleteByOperator(c.Request.Context(), id); err != nil {
			internalErr(c, err)
			return
		}
		deleted, err := repo.Delete(c.Request.Context(), id)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !deleted {
			appErr(c, apperr.OperatorNotFound)
			return
		}
		setResourceID(c, target.Email)
		c.Status(http.StatusNoContent)
	})

	// PUT /operators/:id — superadmin only (this group already requires
	// superadmin via the router mount).
	ops.PUT("/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			badRequest(c, "invalid operator id")
			return
		}
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			RoleID   *int64 `json:"role_id"` // Phase-2: change operator's role
			TenantID *int64 `json:"tenant_id"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Email == "" && body.Password == "" && body.RoleID == nil && body.TenantID == nil {
			badRequest(c, "at least one of email, password, role_id, or tenant_id must be provided")
			return
		}

		// If role_id is set, validate it exists and check scope/tenant coherence.
		if body.RoleID != nil {
			roleRepo := store.NewRoleRepo(db)
			roleObj, ok, err := roleRepo.GetByID(c.Request.Context(), *body.RoleID)
			if err != nil {
				internalErr(c, err)
				return
			}
			if !ok {
				badRequest(c, "role not found")
				return
			}
			// Use the effective tenant_id: if not being changed, the existing one.
			effectiveTenant := body.TenantID
			if effectiveTenant == nil {
				target, _, _ := operatorByID(c.Request.Context(), db, id)
				effectiveTenant = target.TenantID
			}
			if roleObj.ScopeKind == string(authz.ScopeGlobal) && effectiveTenant != nil {
				badRequest(c, "global role must not have a tenant_id")
				return
			}
			if roleObj.ScopeKind == string(authz.ScopeTenant) && effectiveTenant == nil {
				badRequest(c, "tenant role requires a tenant_id")
				return
			}
			// Protect: cannot change the last super-admin to a non-super-admin role.
			target, _, _ := operatorByID(c.Request.Context(), db, id)
			if target.Role == operator.RoleSuperAdmin && roleObj.Name != "super-admin" {
				n, err := repo.CountByRole(c.Request.Context(), operator.RoleSuperAdmin)
				if err != nil {
					internalErr(c, err)
					return
				}
				if n <= 1 {
					badRequest(c, "cannot change the last super-admin to a different role")
					return
				}
			}
		}

		// Tenant coherence: if tenant_id is set to non-nil, verify it is valid
		// for a tenant-admin role.
		var hash string
		if body.Password != "" {
			var err error
			hash, err = operator.HashPassword(body.Password)
			if err != nil {
				internalErr(c, err)
				return
			}
		}

		updated, err := repo.Update(c.Request.Context(), id, body.Email, hash, body.RoleID, body.TenantID)
		if err != nil {
			if isConstraintViolation(err) {
				badRequest(c, "operator update rejected: "+err.Error())
				return
			}
			internalErr(c, err)
			return
		}
		if !updated {
			appErr(c, apperr.OperatorNotFound)
			return
		}

		setResourceID(c, body.Email)
		// Re-fetch to return the updated operator info.
		op, found, err := operatorByID(c.Request.Context(), db, id)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !found {
			appErr(c, apperr.OperatorNotFound)
			return
		}
		c.JSON(http.StatusOK, store.OperatorInfo{
			ID: op.ID, Email: op.Email, Role: string(op.Role), TenantID: op.TenantID,
		})
	})

}

// mountSelfPassword wires the self-service password change endpoint. Any
// authenticated operator can change their own password (no RBAC restriction).
func mountSelfPassword(g *gin.RouterGroup, db *store.DB) {
	g.POST("/operators/me/password", func(c *gin.Context) {
		var body struct {
			Password string `json:"password"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Password == "" {
			badRequest(c, "password is required")
			return
		}

		op := operatorFrom(c)
		if op.ID == 0 {
			appErr(c, apperr.OperatorNotFound)
			return
		}

		hash, err := operator.HashPassword(body.Password)
		if err != nil {
			internalErr(c, err)
			return
		}

		repo := store.NewOperatorRepo(db)
		updated, err := repo.Update(c.Request.Context(), op.ID, "", hash, nil, nil)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !updated {
			appErr(c, apperr.OperatorNotFound)
			return
		}

		c.Status(http.StatusNoContent)
	})
}

// operatorByID looks up an operator's non-secret identity by id (for delete
// guards). ok is false when the id is unknown.
func operatorByID(ctx context.Context, db *store.DB, id int64) (operator.Operator, bool, error) {
	var row struct {
		ID       int64
		Email    string
		Role     string
		TenantID *int64
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT id, email, role, tenant_id FROM operators WHERE id = ?`, id,
	).Scan(&row).Error; err != nil {
		return operator.Operator{}, false, err
	}
	if row.ID == 0 {
		return operator.Operator{}, false, nil
	}
	return operator.Operator{ID: row.ID, Email: row.Email, Role: operator.Role(row.Role), TenantID: row.TenantID}, true, nil
}

// isConstraintViolation reports whether err is a Postgres integrity violation
// (unique/foreign-key/check) — i.e. a client error, not a server fault.
func isConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") || // unique_violation
		strings.Contains(msg, "SQLSTATE 23503") || // foreign_key_violation
		strings.Contains(msg, "SQLSTATE 23514") // check_violation
}
