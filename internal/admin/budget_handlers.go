package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/authz"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

func mountBudgets(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	read := budgetAccess(db, authz.PermBudgetRead)
	write := budgetAccess(db, authz.PermBudgetWrite)
	resolve := budgetAccess(db, authz.PermBudgetResolve)
	g.GET("/budgets", read, func(c *gin.Context) {
		rows, next, err := budgetRepo(c, db).List(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if budgetError(c, err) {
			return
		}
		if rows == nil {
			rows = []billing.BudgetPolicy{}
		}
		c.JSON(http.StatusOK, listEnvelope(rows, next))
	})
	g.POST("/budgets", write, auth.auditMutation("budget", resourceIDFrom), func(c *gin.Context) {
		var body struct {
			billing.BudgetSpec
			Limit *int64 `json:"limit"`
		}
		if !bindBudgetBody(c, &body) {
			return
		}
		if body.Limit == nil {
			appErr(c, apperr.BudgetInvalidBody)
			return
		}
		spec := body.BudgetSpec
		spec.Limit = *body.Limit
		if spec.Timezone == "" {
			spec.Timezone = "UTC"
		}
		if spec.Thresholds == nil {
			spec.Thresholds = []int{}
		}
		if !validBudgetSpec(spec) {
			appErr(c, apperr.BudgetInvalidPolicy)
			return
		}
		row, err := budgetRepo(c, db).Create(c.Request.Context(), spec)
		if budgetError(c, err) {
			return
		}
		setResourceID(c, strconv.FormatInt(row.ID, 10))
		c.JSON(http.StatusCreated, row)
	})
	g.GET("/budgets/:id", read, func(c *gin.Context) {
		id, ok := budgetPolicyID(c)
		if !ok {
			return
		}
		row, err := budgetRepo(c, db).Get(c.Request.Context(), id)
		if budgetError(c, err) {
			return
		}
		c.JSON(http.StatusOK, row)
	})
	g.PATCH("/budgets/:id", write, auth.auditMutation("budget", resourceIDFrom), func(c *gin.Context) {
		id, ok := budgetPolicyID(c)
		if !ok {
			return
		}
		var patch billing.BudgetPatch
		if !bindBudgetBody(c, &patch) {
			return
		}
		if patch.Version < 1 || (patch.Limit == nil && patch.Enabled == nil) || (patch.Limit != nil && *patch.Limit < 0) {
			appErr(c, apperr.BudgetInvalidPolicy)
			return
		}
		row, err := budgetRepo(c, db).Patch(c.Request.Context(), id, patch)
		if budgetError(c, err) {
			return
		}
		setResourceID(c, strconv.FormatInt(row.ID, 10))
		c.JSON(http.StatusOK, row)
	})
	g.GET("/budgets/:id/accounts", read, func(c *gin.Context) {
		id, ok := budgetPolicyID(c)
		if !ok {
			return
		}
		rows, err := budgetRepo(c, db).Accounts(c.Request.Context(), id)
		if budgetError(c, err) {
			return
		}
		if rows == nil {
			rows = []billing.BudgetAccount{}
		}
		c.JSON(http.StatusOK, listEnvelope(rows, ""))
	})
	g.GET("/budget-events", read, func(c *gin.Context) {
		rows, next, err := budgetRepo(c, db).Events(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if budgetError(c, err) {
			return
		}
		if rows == nil {
			rows = []billing.BudgetEvent{}
		}
		c.JSON(http.StatusOK, listEnvelope(rows, next))
	})
	g.GET("/billing-reservations", read, func(c *gin.Context) {
		status := c.Query("status")
		switch status {
		case "", "reserved", "dispatched", "unknown", "settled", "released", "released_unknown":
		default:
			appErr(c, apperr.BudgetInvalidQuery)
			return
		}
		rows, next, err := budgetRepo(c, db).Reservations(c.Request.Context(), status, c.Query("cursor"), parseLimit(c))
		if budgetError(c, err) {
			return
		}
		if rows == nil {
			rows = []billing.Reservation{}
		}
		c.JSON(http.StatusOK, listEnvelope(rows, next))
	})
	g.POST("/billing-reservations/:id/resolve", resolve, auth.auditMutation("billing_reservation", resourceIDFrom), func(c *gin.Context) {
		var resolution billing.Resolution
		if !bindBudgetBody(c, &resolution) {
			return
		}
		if resolution.Version < 1 || strings.TrimSpace(resolution.Reason) == "" || strings.TrimSpace(resolution.Evidence) == "" {
			appErr(c, apperr.BudgetInvalidPolicy)
			return
		}
		switch resolution.Action {
		case "settle":
			if resolution.Actual == nil || *resolution.Actual < 0 {
				appErr(c, apperr.BudgetInvalidPolicy)
				return
			}
		case "release", "release_unknown":
			if resolution.Actual != nil {
				appErr(c, apperr.BudgetInvalidPolicy)
				return
			}
		default:
			appErr(c, apperr.BudgetInvalidPolicy)
			return
		}
		resolution.OperatorID = operatorFrom(c).ID
		row, err := budgetRepo(c, db).Resolve(c.Request.Context(), c.Param("id"), resolution)
		if budgetError(c, err) {
			return
		}
		setResourceID(c, row.ID)
		c.JSON(http.StatusOK, row)
	})
}

// This gate intentionally bypasses the legacy api_key.read tenant-admin gate.
// Permission and structural scope are checked before resolving a tenant or body.
func budgetAccess(db *store.DB, permission authz.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		op := operatorFrom(c)
		if !op.Permissions[string(authz.Wildcard)] && !op.Permissions[string(permission)] {
			appErr(c, apperr.PermissionDenied)
			return
		}
		if permission != authz.PermBudgetRead && (op.TenantID != nil || op.ScopeKind != string(authz.ScopeGlobal)) {
			appErr(c, apperr.PermissionDenied)
			return
		}
		tenantName := c.Query("tenant")
		var tenantID int64
		if op.TenantID != nil {
			if op.ScopeKind != string(authz.ScopeTenant) {
				appErr(c, apperr.PermissionDenied)
				return
			}
			tenantID = *op.TenantID
			name, err := store.TenantName(c.Request.Context(), db, tenantID)
			if err != nil {
				appErr(c, apperr.Unexpected)
				return
			}
			if name == "" {
				appErr(c, apperr.TenantNotFound)
				return
			}
			if tenantName != "" && tenantName != name {
				appErr(c, apperr.BudgetTenantMismatch)
				return
			}
			tenantName = name
		} else {
			if op.ScopeKind != string(authz.ScopeGlobal) {
				appErr(c, apperr.PermissionDenied)
				return
			}
			if tenantName == "" {
				appErr(c, apperr.BudgetTenantRequired)
				return
			}
			if err := db.WithContext(c.Request.Context()).Raw(`SELECT id FROM tenants WHERE name = ?`, tenantName).Scan(&tenantID).Error; err != nil {
				appErr(c, apperr.Unexpected)
				return
			}
			if tenantID == 0 {
				appErr(c, apperr.TenantNotFound)
				return
			}
		}
		c.Set("emg.budget_tenant_id", tenantID)
		c.Set("emg.audit_tenant", tenantName)
		// Limit before auditMutation captures the body, not only at JSON decode.
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		}
		c.Next()
	}
}

func budgetRepo(c *gin.Context, db *store.DB) *store.BudgetRepo {
	return store.NewBudgetRepo(db, c.GetInt64("emg.budget_tenant_id"))
}

func budgetPolicyID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		appErr(c, apperr.BudgetInvalidQuery)
		return 0, false
	}
	return id, true
}

func bindBudgetBody(c *gin.Context, dst any) bool {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		appErr(c, apperr.BudgetInvalidBody)
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		appErr(c, apperr.BudgetInvalidBody)
		return false
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			appErr(c, apperr.BudgetInvalidBody)
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		appErr(c, apperr.BudgetInvalidBody)
		return false
	}
	return true
}

func validBudgetSpec(spec billing.BudgetSpec) bool {
	if strings.TrimSpace(spec.Name) == "" || spec.Limit < 0 || (spec.Mode != "enforce" && spec.Mode != "soft") {
		return false
	}
	if len(spec.Currency) != 3 {
		return false
	}
	for _, ch := range spec.Currency {
		if ch < 'a' || ch > 'z' {
			return false
		}
	}
	if _, _, err := billing.PeriodBounds(time.Now(), spec.Period, spec.Timezone); err != nil {
		return false
	}
	switch spec.ScopeKind {
	case "tenant":
		if spec.ScopeRef != "" {
			return false
		}
	case "group", "application", "application_env":
		id, err := strconv.ParseInt(spec.ScopeRef, 10, 64)
		if err != nil || id < 1 {
			return false
		}
	case "key":
		if strings.TrimSpace(spec.ScopeRef) == "" {
			return false
		}
	default:
		return false
	}
	if spec.ScopeKind == "application_env" {
		if spec.Environment != "dev" && spec.Environment != "staging" && spec.Environment != "prod" {
			return false
		}
	} else if spec.Environment != "" {
		return false
	}
	seen := make(map[int]bool, len(spec.Thresholds))
	for _, threshold := range spec.Thresholds {
		if threshold < 1 || threshold > 100 || seen[threshold] {
			return false
		}
		seen[threshold] = true
	}
	return true
}

func budgetError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, billing.ErrNotFound):
		appErr(c, apperr.BudgetNotFound)
	case errors.Is(err, billing.ErrInvalidPolicy):
		appErr(c, apperr.BudgetInvalidPolicy)
	case errors.Is(err, billing.ErrReservationConflict):
		appErr(c, apperr.BudgetConflict)
	case errors.Is(err, billing.ErrCurrencyMismatch):
		appErr(c, apperr.BudgetCurrencyMismatch)
	case errors.Is(err, billing.ErrInsufficient):
		appErr(c, apperr.BudgetInsufficient)
	default:
		appErr(c, apperr.Unexpected)
	}
	return true
}
