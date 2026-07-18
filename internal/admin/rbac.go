package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/authz"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// sessionTTL bounds how long an operator session is valid.
const sessionTTL = 12 * time.Hour

// lockout parameters: after maxLoginFailures within lockoutWindow for an email,
// logins for that email are refused (429) until the window elapses (ADR-0017).
const (
	maxLoginFailures = 5
	lockoutWindow    = 5 * time.Minute
)

// operatorCtxKey stores the resolved operator in the gin context.
const operatorCtxKey = "emg.operator"

// rbac wires operator authn/authz over the store repos.
type rbac struct {
	db        *store.DB
	operators *store.OperatorRepo
	sessions  *store.SessionRepo
	audit     *store.AuditRepo

	mu       sync.Mutex
	failures map[string]*failCounter // email → recent failed logins
}

type failCounter struct {
	count int
	first time.Time
}

func newRBAC(db *store.DB) *rbac {
	return &rbac{
		db:        db,
		operators: store.NewOperatorRepo(db),
		sessions:  store.NewSessionRepo(db),
		audit:     store.NewAuditRepo(db),
		failures:  map[string]*failCounter{},
	}
}

// login authenticates email+password and issues a session token. It enforces a
// per-email failed-attempt lockout.
func (a *rbac) login(c *gin.Context) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		appErr(c, apperr.InvalidBody)
		return
	}

	if a.lockedOut(body.Email) {
		appErr(c, apperr.TooManyLogins)
		return
	}

	op, hash, ok, err := a.operators.GetByEmail(c.Request.Context(), body.Email)
	if err != nil {
		appErrMsg(c, apperr.Unexpected, err.Error())
		return
	}
	valid := false
	if ok {
		valid, _ = operator.VerifyPassword(body.Password, hash)
	}
	if !valid {
		a.recordFailure(body.Email)
		// Uniform 401 whether the email is unknown or the password is wrong
		// (don't leak which emails exist).
		appErr(c, apperr.InvalidCredentials)
		return
	}
	a.clearFailures(body.Email)

	token, err := newSessionToken()
	if err != nil {
		appErrMsg(c, apperr.Unexpected, "session token")
		return
	}
	if err := a.sessions.Create(c.Request.Context(), token, op.ID, time.Now().Add(sessionTTL)); err != nil {
		appErrMsg(c, apperr.Unexpected, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// authnMiddleware resolves the Bearer session token to an operator and stores it
// in the context. Missing/invalid/expired sessions are rejected 401.
func (a *rbac) authnMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearer(c)
		if !ok {
			appErr(c, apperr.MissingBearerToken)
			return
		}
		op, ok, err := a.sessions.Lookup(c.Request.Context(), token)
		if err != nil {
			appErrMsg(c, apperr.Unexpected, err.Error())
			return
		}
		if !ok {
			appErr(c, apperr.InvalidSession)
			return
		}
		c.Set(operatorCtxKey, op)
		c.Next()
	}
}

// requireSuperAdmin authorizes global-config routes: the operator must hold the
// wildcard permission (i.e. be a super-admin). Under Phase-2 RBAC this is
// equivalent to checking for authz.Wildcard in the loaded permission set.
// Must run after authnMiddleware.
func (a *rbac) requireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		op := operatorFrom(c)
		if op.Permissions == nil || !op.Permissions[string(authz.Wildcard)] {
			appErr(c, apperr.SuperAdminRequired)
			return
		}
		c.Next()
	}
}

// requireTenantAdmin authorizes tenant-scoped routes: the operator must be bound
// to a tenant (tenant_id IS NOT NULL) and hold at least one tenant-scoped
// permission. super-admin (wildcard) cannot pass — global operators must not
// mix into tenant-scoped routes. Must run after authnMiddleware.
func (a *rbac) requireTenantAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		op := operatorFrom(c)
		if op.TenantID == nil {
			appErr(c, apperr.TenantAdminRequired)
			return
		}
		// Under Phase-2: the operator must hold at least one tenant-scoped or
		// both-scoped permission (not just be any operator with a tenant_id).
		if op.Permissions == nil || !op.Permissions[string(authz.PermAPIKeyRead)] {
			appErr(c, apperr.TenantAdminRequired)
			return
		}
		c.Next()
	}
}

// (2xx). Wired as middleware so it cannot be forgotten per-handler (ADR-0017
// §5). resourceType is fixed per route group; resourceID + payload come from the
// request. Reads (GET) are never audited.
func (a *rbac) auditMutation(resourceType string, idFrom func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		// Capture the JSON body for the After snapshot (create/update).
		var after any
		if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch {
			if b, err := c.GetRawData(); err == nil && len(b) > 0 {
				// Re-seat the body so the handler can bind it.
				c.Request.Body = reusableBody(b)
				after = rawJSON(b)
			}
		}

		c.Next()

		if c.Writer.Status() < 200 || c.Writer.Status() >= 300 {
			return // only audit successful mutations
		}
		action := actionFor(c.Request.Method)
		op := operatorFrom(c)
		resourceID := idFrom(c)
		_ = a.audit.Record(c.Request.Context(), store.AuditEntry{
			OperatorID:   &op.ID,
			Action:       action,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Tenant:       a.affectedTenant(c.Request.Context(), resourceType, resourceID, op),
			After:        after,
		})
	}
}

// affectedTenant resolves which tenant an audited mutation affects (ADR-0019),
// so tenant-admins can read the trail of operations against their tenant —
// including super-admin actions on it. Returns nil for global/platform actions
// with no single owning tenant.
//
//   - provider/model/route/plugin: global config → nil
//   - tenant: the tenant itself; resourceID is its name → that name
//   - api_key/group: owned by the acting operator's tenant → that tenant's name
//   - quota: scope "tenant:X" attributes to X; "group:X/..." to X; others → nil
func (a *rbac) affectedTenant(ctx context.Context, resourceType, resourceID string, op operator.Operator) *string {
	switch resourceType {
	case "tenant":
		if resourceID == "" {
			return nil
		}
		name := resourceID
		return &name
	case "api_key", "group":
		// These endpoints are tenant-admin scoped; the affected tenant is the
		// operator's own tenant.
		if op.TenantID == nil {
			return nil
		}
		name, err := store.TenantName(ctx, a.db, *op.TenantID)
		if err != nil || name == "" {
			return nil
		}
		return &name
	case "quota":
		if t := tenantFromScope(resourceID); t != "" {
			return &t
		}
		return nil
	case "operator":
		// resourceID is the operator's email. The affected tenant is that
		// operator's own tenant (a tenant-admin belongs to one tenant; a
		// super-admin is global → nil). Resolvable post-create by lookup; on
		// delete the row is already gone, so this degrades to nil (global) —
		// the create case is the one a tenant-admin cares to see.
		targetOp, _, ok, err := a.operators.GetByEmail(ctx, resourceID)
		if err != nil || !ok || targetOp.TenantID == nil {
			return nil
		}
		name, err := store.TenantName(ctx, a.db, *targetOp.TenantID)
		if err != nil || name == "" {
			return nil
		}
		return &name
	case "role":
		// Roles are global config; no single owning tenant.
		return nil
	default:
		// provider/model/route/plugin and anything else: global.
		return nil
	}
}

// tenantFromScope extracts the tenant name from a quota scope key. Scopes are
// "tenant:<name>", "group:<tenant>/<group>", or "key:<id>". Only the first two
// carry a tenant; a bare key scope has none at this layer.
func tenantFromScope(scope string) string {
	switch {
	case strings.HasPrefix(scope, "tenant:"):
		return strings.TrimPrefix(scope, "tenant:")
	case strings.HasPrefix(scope, "group:"):
		rest := strings.TrimPrefix(scope, "group:")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return rest[:i]
		}
		return rest
	default:
		return ""
	}
}

// --- lockout helpers ---

func (a *rbac) lockedOut(email string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	fc, ok := a.failures[email]
	if !ok {
		return false
	}
	if time.Since(fc.first) > lockoutWindow {
		delete(a.failures, email)
		return false
	}
	return fc.count >= maxLoginFailures
}

func (a *rbac) recordFailure(email string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fc, ok := a.failures[email]
	if !ok || time.Since(fc.first) > lockoutWindow {
		a.failures[email] = &failCounter{count: 1, first: time.Now()}
		return
	}
	fc.count++
}

func (a *rbac) clearFailures(email string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, email)
}

// --- small helpers ---

func operatorFrom(c *gin.Context) operator.Operator {
	v, _ := c.Get(operatorCtxKey)
	op, _ := v.(operator.Operator)
	return op
}

func bearer(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || h[:len(p)] != p {
		return "", false
	}
	return h[len(p):], true
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func actionFor(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return method
	}
}

// reusableBody wraps captured bytes as a fresh ReadCloser so a handler can bind
// the body after the audit middleware read it.
func reusableBody(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}

// rawJSON returns the bytes as json.RawMessage when they are valid JSON, else
// nil (so a non-JSON body is simply not snapshotted).
func rawJSON(b []byte) any {
	if !json.Valid(b) {
		return nil
	}
	return json.RawMessage(b)
}
