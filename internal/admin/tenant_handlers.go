package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/store"
)

// mountTenantAdmin wires super-admin tenancy management (create/list/enable-
// toggle tenants). Audited as resource_type "tenant".
func mountTenantAdmin(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	tenants := g.Group("/tenants", auth.auditMutation("tenant", resourceIDFrom))
	tenants.POST("", func(c *gin.Context) {
		var body struct {
			Name string `json:"name"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Name == "" {
			badRequest(c, "tenant name is required")
			return
		}
		id, err := store.CreateTenant(c.Request.Context(), db, body.Name)
		if err != nil {
			// Unique-violation (duplicate name) → client error, mirrors
			// mountOperators' create handler.
			if isConstraintViolation(err) {
				badRequest(c, "tenant create rejected: "+err.Error())
				return
			}
			internalErr(c, err)
			return
		}
		setResourceID(c, body.Name)
		c.JSON(http.StatusCreated, gin.H{"id": id, "name": body.Name, "enabled": true})
	})
	tenants.GET("", func(c *gin.Context) {
		list, next, err := store.ListTenants(c.Request.Context(), db, c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, next))
	})
	// PATCH toggles enabled — reversible, unlike api_keys.revoked_at: a
	// disabled tenant can be re-enabled. Enforced at the data-plane auth
	// boundary (KeyRepo.LookupByHash), so every API key under a disabled
	// tenant is rejected without any cascading write to other tables.
	tenants.PATCH("/:name", func(c *gin.Context) {
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
		ok, err := store.SetTenantEnabled(c.Request.Context(), db, name, *body.Enabled)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.TenantNotFound)
			return
		}
		setResourceID(c, name)
		c.JSON(http.StatusOK, gin.H{"name": name, "enabled": *body.Enabled})
	})
}

// mountTenantScoped wires tenant-admin resources over the tenant-scoped repo.
// The scope is the operator's own tenant, injected structurally (ADR-0017 §3),
// so a tenant-admin cannot touch another tenant's keys.
func mountTenantScoped(g *gin.RouterGroup, db *store.DB, auth *rbac) {
	keys := g.Group("/api-keys", auth.auditMutation("api_key", resourceIDFrom))
	keys.POST("", func(c *gin.Context) {
		op := operatorFrom(c)
		var body struct {
			KeyID         string   `json:"key_id"`
			AllowedModels []string `json:"allowed_models"`
		}
		if !bind(c, &body) {
			return
		}
		if body.KeyID == "" {
			badRequest(c, "key_id is required")
			return
		}

		// Validate allowed_models against existing model aliases.
		for _, m := range body.AllowedModels {
			var exists bool
			if err := db.WithContext(c.Request.Context()).Raw(
				`SELECT EXISTS (SELECT 1 FROM models WHERE alias = ?)`, m,
			).Scan(&exists).Error; err != nil {
				internalErr(c, err)
				return
			}
			if !exists {
				badRequest(c, "unknown model in allowed_models: "+m)
				return
			}
		}

		// Generate the client key; store only its SHA-256 hash (ADR-0006). The
		// plaintext is returned once, here, and never again.
		plaintext, err := newClientKey()
		if err != nil {
			internalErr(c, err)
			return
		}
		sum := sha256.Sum256([]byte(plaintext))
		hash := hex.EncodeToString(sum[:])

		repo := store.NewTenantRepo(db, *op.TenantID)
		if err := repo.CreateAPIKey(c.Request.Context(), store.APIKeySpec{
			KeyID: body.KeyID, Hash: hash, AllowedModels: body.AllowedModels,
		}); err != nil {
			if isConstraintViolation(err) {
				badRequest(c, "api_key create rejected: "+err.Error())
				return
			}
			internalErr(c, err)
			return
		}
		setResourceID(c, body.KeyID)
		c.JSON(http.StatusCreated, gin.H{"key_id": body.KeyID, "api_key": plaintext})
	})
	keys.GET("", func(c *gin.Context) {
		op := operatorFrom(c)
		repo := store.NewTenantRepo(db, *op.TenantID)
		list, next, err := repo.ListAPIKeys(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, next))
	})
	keys.DELETE("/:key_id", func(c *gin.Context) {
		op := operatorFrom(c)
		repo := store.NewTenantRepo(db, *op.TenantID)
		revoked, err := repo.RevokeAPIKey(c.Request.Context(), c.Param("key_id"))
		if err != nil {
			internalErr(c, err)
			return
		}
		if !revoked {
			appErr(c, apperr.APIKeyNotFoundInTenant)
			return
		}
		setResourceID(c, c.Param("key_id"))
		c.Status(http.StatusNoContent)
	})
	// PATCH /api-keys/:key_id updates allowed_models for an existing key (tenant-
	// admin only, scoped to own tenant). The key must be active (non-revoked).
	keys.PATCH("/:key_id", func(c *gin.Context) {
		op := operatorFrom(c)
		var body struct {
			AllowedModels []string `json:"allowed_models"`
		}
		if !bind(c, &body) {
			return
		}
		if len(body.AllowedModels) == 0 {
			badRequest(c, "allowed_models must be a non-empty array; use all-models (empty) for no restriction")
			return
		}

		// Validate each model exists (same check as POST create).
		for _, m := range body.AllowedModels {
			var exists bool
			if err := db.WithContext(c.Request.Context()).Raw(
				`SELECT EXISTS (SELECT 1 FROM models WHERE alias = ?)`, m,
			).Scan(&exists).Error; err != nil {
				internalErr(c, err)
				return
			}
			if !exists {
				badRequest(c, "unknown model in allowed_models: "+m)
				return
			}
		}

		repo := store.NewTenantRepo(db, *op.TenantID)
		ok, err := repo.SetAPIKeyAllowedModels(c.Request.Context(), c.Param("key_id"), body.AllowedModels)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.APIKeyNotFoundInTenant)
			return
		}
		setResourceID(c, c.Param("key_id"))
		c.Status(http.StatusNoContent)
	})

	// Groups — tenant-admin scoped, audited as resource_type "group"
	// (rbac.go:214 already handles the "group" case in affectedTenant).
	groups := g.Group("/groups", auth.auditMutation("group", resourceIDFrom))
	groups.POST("", func(c *gin.Context) {
		op := operatorFrom(c)
		var body struct {
			Name string `json:"name"`
		}
		if !bind(c, &body) {
			return
		}
		if body.Name == "" {
			badRequest(c, "group name is required")
			return
		}
		repo := store.NewTenantRepo(db, *op.TenantID)
		id, err := repo.CreateGroup(c.Request.Context(), body.Name)
		if err != nil {
			if isConstraintViolation(err) {
				badRequest(c, "group create rejected: "+err.Error())
				return
			}
			internalErr(c, err)
			return
		}
		setResourceID(c, body.Name)
		c.JSON(http.StatusCreated, gin.H{"id": id, "name": body.Name, "enabled": true})
	})
	groups.GET("", func(c *gin.Context) {
		op := operatorFrom(c)
		repo := store.NewTenantRepo(db, *op.TenantID)
		list, next, err := repo.ListGroups(c.Request.Context(), c.Query("cursor"), parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, next))
	})
	// PATCH toggles enabled — reversible, mirrors tenant pattern.
	groups.PATCH("/:name", func(c *gin.Context) {
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
		repo := store.NewTenantRepo(db, *op.TenantID)
		ok, err := repo.SetGroupEnabled(c.Request.Context(), name, *body.Enabled)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.GroupNotFound)
			return
		}
		setResourceID(c, name)
		c.JSON(http.StatusOK, gin.H{"name": name, "enabled": *body.Enabled})
	})
	// DELETE rejects when api_keys reference the group (409), mirrors the
	// provider/model delete-with-refs pattern.
	groups.DELETE("/:name", func(c *gin.Context) {
		op := operatorFrom(c)
		name := c.Param("name")
		repo := store.NewTenantRepo(db, *op.TenantID)
		refs, err := repo.GroupReferencedByAPIKeys(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if len(refs) > 0 {
			appErrMsg(c, apperr.GroupReferenced,
				"api_key(s) "+strings.Join(refs, ", ")+"; revoke or repoint them first")
			return
		}
		ok, err := repo.DeleteGroup(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.GroupNotFound)
			return
		}
		setResourceID(c, name)
		c.Status(http.StatusNoContent)
	})
}

// newClientKey generates a random client API key (plaintext), prefixed for
// recognizability.
func newClientKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(b), nil
}
