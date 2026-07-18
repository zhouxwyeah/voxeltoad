// Package admin is the management-plane core: REST API for providers, models,
// routes, and plugins, persisted to PostgreSQL, plus the config snapshot the
// data plane polls. Cross-references (route→provider, model upstream→provider)
// are validated at the write boundary (ADR-0014); every config write bumps the
// snapshot version (ADR-0015).
package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/authz"
	"voxeltoad/internal/config"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/store"
)

// InternalTokenHeader is the header carrying the shared internal-trust secret
// on the snapshot channel (ADR-0007). Aliased from config so both planes share
// one definition.
const InternalTokenHeader = config.InternalTokenHeader

// Options configures the admin Router.
type Options struct {
	// InternalToken is the shared secret required on the config-snapshot
	// endpoint (ADR-0007). Empty disables the gate (dev/test only).
	InternalToken string
	// DB is the PostgreSQL connection backing config CRUD and the snapshot. When
	// nil, CRUD is unavailable and the snapshot serves an empty "v0" (skeleton/
	// dev mode).
	DB *store.DB
	// AllowedOrigins enables CORS for the listed browser origins (ADR-0019:
	// front-end/back-end separation). Empty = CORS disabled (same-origin only).
	AllowedOrigins []string
	// CredentialService encrypts/decrypts provider credentials. When nil, the
	// admin plane cannot write encrypted credentials, but it can still serve
	// providers that use env:// or plain:// refs.
	CredentialService credential.Service
	// CredentialRepo persists encrypted provider credentials. Required when
	// CredentialService is non-nil.
	CredentialRepo *store.CredentialRepo
}

// Router builds the admin-plane HTTP handler.
func Router(opts Options) http.Handler {
	r := gin.New()
	r.Use(gin.Recovery())
	if len(opts.AllowedOrigins) > 0 {
		r.Use(corsMiddleware(opts.AllowedOrigins))
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	var repo *store.ConfigRepo
	var snapRepo *store.ConfigSnapshotRepo
	if opts.DB != nil {
		snapRepo = store.NewConfigSnapshotRepo(opts.DB)
		repo = store.NewConfigRepo(opts.DB, snapRepo)
	}

	// Config snapshot consumed by the data plane (see internal/config.Poller).
	// Gated by the shared internal-trust secret when configured (ADR-0007).
	// Supports conditional GET via If-None-Match → 304.
	r.GET("/internal/config/snapshot", requireInternalToken(opts.InternalToken), snapshotHandler(repo))

	// Admin REST surface. CRUD requires a DB and operator auth (ADR-0017).
	v1 := r.Group("/api/v1")
	if opts.DB != nil {
		auth := newRBAC(opts.DB)
		// Operator login issues a session token (unauthenticated endpoint).
		r.POST("/auth/login", auth.login)

		// Everything under /api/v1 requires an authenticated operator; authz is
		// then applied per resource group. authn/authz/audit are middleware so no
		// handler can bypass them (ADR-0017 §§1,3,5).
		v1.Use(auth.authnMiddleware())

		// Self-service endpoints for authenticated operators (no RBAC
		// restriction beyond authentication).
		mountSelfPassword(v1, opts.DB)

		// Global config reads (models): open to any authenticated operator
		// (both super-admin and tenant-admin), since models are global shared
		// config and the API-key form needs model aliases.
		configReadGrp := v1.Group("")

		// Global config writes (providers/routes/plugins/models) + tenancy
		// management: super-admin only.
		globalGrp := v1.Group("", auth.requireSuperAdmin())
		mountConfigCRUD(configReadGrp, globalGrp, repo, opts.CredentialService, opts.CredentialRepo, auth)
		mountConfigHistory(globalGrp, repo, snapRepo)
		mountGatewaySettings(globalGrp, repo, auth) // gateway-wide behavior knobs (trace, etc.)
		mountDataPlaneNodes(globalGrp, store.NewDataPlaneRepo(opts.DB))
		mountOverview(globalGrp, opts.DB)
		mountTenantAdmin(globalGrp, opts.DB, auth)
		mountQuotaAdmin(globalGrp, opts.DB, auth)
		mountOperators(globalGrp, opts.DB, auth)

		// Roles & permissions management (Phase-2 RBAC custom roles).
		roleRepo := store.NewRoleRepo(opts.DB)
		mountPermissions(v1, authz.AllPermissions())
		mountRoles(globalGrp, roleRepo, opts.DB, auth)

		// Tenant-scoped resources (api-keys): tenant-admin, isolated to its own
		// tenant via the scoped repository.
		tenantGrp := v1.Group("", auth.requireTenantAdmin())
		mountTenantScoped(tenantGrp, opts.DB, auth)

		// Usage reads serve both roles (super-admin global / tenant-admin own
		// tenant); scoping is resolved from the operator inside the handler, so
		// these mount on the authenticated group without a role gate.
		mountUsage(v1, opts.DB, auth)
		mountAudit(v1, opts.DB)
		mountRequestLogs(v1, opts.DB)
		mountTrace(v1, opts.DB) // LLM trace message+raw layers (ADR-0039)
		mountQuotaRead(v1, opts.DB)
		mountMe(v1, opts.DB)
	} else {
		for _, p := range []string{"/providers", "/models", "/routes", "/plugins", "/api-keys", "/quotas", "/groups", "/me"} {
			v1.GET(p, notImplemented)
		}
	}

	return r
}

// snapshotHandler serves the assembled config snapshot with a config_generation
// ETag and conditional-GET support. With no repo (dev mode) it serves an empty
// "v0".
func snapshotHandler(repo *store.ConfigRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		if repo == nil {
			const version = "v0"
			if c.GetHeader("If-None-Match") == version {
				c.Status(http.StatusNotModified)
				return
			}
			c.Header("ETag", version)
			c.JSON(http.StatusOK, gin.H{"version": version, "raw": nil})
			return
		}

		snap, err := repo.Snapshot(c.Request.Context())
		if err != nil {
			appErrMsg(c, apperr.SnapshotFailed, err.Error())
			return
		}
		if c.GetHeader("If-None-Match") == snap.Version {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("ETag", snap.Version)
		c.JSON(http.StatusOK, snap)
	}
}

// requireInternalToken returns middleware that rejects snapshot requests lacking
// the correct shared secret. When token is empty the gate is disabled.
func requireInternalToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		if c.GetHeader(InternalTokenHeader) != token {
			appErr(c, apperr.InvalidInternalToken)
			return
		}
		c.Next()
	}
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "not implemented")
}

func errBody(typ, msg string) gin.H {
	return gin.H{"error": gin.H{"message": msg, "type": typ}}
}

// appErr emits the same {"error":{"message","type"}} envelope as errBody, but
// driven by an apperr.Error. The message is the i18n key (the frontend resolves
// it via errors/<domain>.json); the type is the stable code. Use this in favor
// of inline errBody(...) so each domain lives in its own apperr file.
func appErr(c *gin.Context, e *apperr.Error) {
	c.AbortWithStatusJSON(e.Status, gin.H{"error": gin.H{"message": e.I18n, "type": e.Code}})
}

// appErrMsg is appErr when the handler needs to append runtime context to the
// message (e.g. the underlying cause). The i18n key is still used as the base
// message, with ctx appended; type is the stable code.
func appErrMsg(c *gin.Context, e *apperr.Error, ctx string) {
	msg := e.I18n
	if ctx != "" {
		msg = msg + ": " + ctx
	}
	c.AbortWithStatusJSON(e.Status, gin.H{"error": gin.H{"message": msg, "type": e.Code}})
}

// listEnvelope wraps a list payload in the uniform {data, next_cursor} envelope
// (ADR-0019). nextCursor is "" when there are no further pages. data is emitted
// as an empty array (never null) when the slice is empty, so clients can always
// iterate .data.
func listEnvelope(data any, nextCursor string) gin.H {
	return gin.H{"data": data, "next_cursor": nextCursor}
}

// offsetListEnvelope wraps a page payload for offset-paginated endpoints (audit
// & request-logs page-jump UI). It is intentionally separate from listEnvelope
// to avoid perturbing the 13 keyset endpoints that still rely on the
// {data, next_cursor} ADR-0019 contract.
func offsetListEnvelope(data any, total int64, page, pageSize int) gin.H {
	return gin.H{
		"data":      data,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}

// corsMiddleware grants CORS to the configured origins only. A preflight
// (OPTIONS) from an allowed origin is answered 204 immediately. Requests from
// other origins pass through without CORS grant headers (same-origin still
// works; cross-origin browsers block per the missing header).
func corsMiddleware(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowed[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+InternalTokenHeader)
			h.Set("Access-Control-Allow-Credentials", "true")
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}
