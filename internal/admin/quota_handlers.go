package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// mountQuotaAdmin wires super-admin quota top-up (ADR-0019). Top-up is an atomic
// increment (balance += delta), never an overwrite, so it cannot clobber a
// concurrent hot-path debit. Audited as resource_type "quota" (ADR-0017 §5).
func mountQuotaAdmin(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	repo := store.NewQuotaRepo(db)

	quotas := g.Group("/quotas", auth.auditMutation("quota", resourceIDFrom))
	quotas.POST("/topup", func(c *gin.Context) {
		var body struct {
			Scope    string `json:"scope"`
			Delta    int64  `json:"delta"`
			Currency string `json:"currency"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Scope == "" {
			badRequest(c, "scope is required")
			return
		}
		if body.Delta <= 0 {
			badRequest(c, "delta must be a positive number of micro-units")
			return
		}
		if err := validateQuotaScope(c.Request.Context(), db, body.Scope); err != nil {
			badRequest(c, err.Error())
			return
		}
		if err := repo.TopUp(c.Request.Context(), body.Scope, body.Delta, body.Currency); err != nil {
			internalErr(c, err)
			return
		}
		bal, err := repo.Balance(c.Request.Context(), body.Scope)
		if err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, body.Scope)
		c.JSON(http.StatusOK, gin.H{"scope": body.Scope, "balance": bal, "currency": body.Currency})
	})
}

// mountQuotaRead wires the read-only balance lookup GET /quotas?scope for BOTH
// roles (ADR-0019). super-admin may read any scope; a tenant-admin may only read
// scopes within its own tenant (tenant:<own> or group:<own>/...), enforced by
// comparing the scope's tenant against the operator's. Reads are not audited.
func mountQuotaRead(g *gin.RouterGroup, db *store.DB) {
	repo := store.NewQuotaRepo(db)
	g.GET("/quotas", func(c *gin.Context) {
		scope := c.Query("scope")
		if scope == "" {
			badRequest(c, "scope query parameter is required")
			return
		}
		op := operatorFrom(c)
		if op.Role == operator.RoleTenantAdmin {
			own := ""
			if op.TenantID != nil {
				name, err := store.TenantName(c.Request.Context(), db, *op.TenantID)
				if err != nil {
					internalErr(c, err)
					return
				}
				own = name
			}
			if own == "" || tenantFromScope(scope) != own {
				appErr(c, apperr.ScopeOutsideTenant)
				return
			}
		}
		bal, cur, err := repo.BalanceWithCurrency(c.Request.Context(), scope)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"scope": scope, "balance": bal, "currency": cur})
	})
}

// validateQuotaScope checks that a tenant-attributed scope refers to an existing
// tenant (preventing typos from silently directing funds into a black hole).
// key:Z and bare strings are not validated — they support pre-funding before the
// key/entity exists (design/domain-flows.md §2.5). group:X/Y validates X exists
// but does not require the group to exist yet.
func validateQuotaScope(ctx context.Context, db *store.DB, scope string) error {
	switch {
	case strings.HasPrefix(scope, "tenant:"):
		name := strings.TrimPrefix(scope, "tenant:")
		if name == "" {
			return fmt.Errorf("invalid scope: empty tenant name")
		}
		return tenantExists(ctx, db, name)
	case strings.HasPrefix(scope, "group:"):
		rest := strings.TrimPrefix(scope, "group:")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return tenantExists(ctx, db, rest[:i])
		}
		// group without /tenant separator — malformed but not preventing operation.
		return nil
	default:
		// key:Z and bare strings — skip validation (pre-funding allowed).
		return nil
	}
}

func tenantExists(ctx context.Context, db *store.DB, name string) error {
	var exists bool
	if err := db.WithContext(ctx).Raw(
		`SELECT EXISTS (SELECT 1 FROM tenants WHERE name = ?)`, name,
	).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tenant %q not found", name)
	}
	return nil
}
