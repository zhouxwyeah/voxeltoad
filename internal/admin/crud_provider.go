package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/adapter"
	_ "voxeltoad/internal/adapter/claude"
	_ "voxeltoad/internal/adapter/openai"
	"voxeltoad/internal/apperr"
	"voxeltoad/internal/config"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/store"
)

// providerRequest is the admin API payload for provider create/update. It
// extends config.Provider with a write-only api_key field that, when supplied,
// is encrypted and stored in provider_credentials (ADR-0031).
type providerRequest struct {
	config.Provider
	// APIKey is a one-time plaintext upstream credential. If non-empty it takes
	// precedence over APIKeyRef and is replaced by db://provider/<name> before
	// persistence. It is never returned.
	APIKey string `json:"api_key,omitempty"`
}

// maskedProvider returns a copy of p with APIKeyRef sanitized so that secrets
// are not returned through the API (ADR-0031).
func maskedProvider(p config.Provider) config.Provider {
	p.APIKeyRef = maskAPIKeyRef(p.APIKeyRef)
	return p
}

// maskAPIKeyRef replaces the sensitive part of an api_key_ref with a mask.
//   - env://VAR_NAME -> env://***
//   - db://provider/<name> -> db://provider/<name> (already safe)
//   - plain://secret -> plain://***
//   - bare literal -> ***
func maskAPIKeyRef(ref string) string {
	if strings.HasPrefix(ref, config.DBProviderRefPrefix) {
		return ref
	}
	if scheme, _, has := strings.Cut(ref, "://"); has {
		return scheme + "://***"
	}
	if ref == "" {
		return ""
	}
	return "***"
}

// mountProviderCRUD wires provider CRUD. Provider deletion checks for model/
// route references and rejects (409) to avoid dangling runtime references.
func mountProviderCRUD(g *gin.RouterGroup, repo *store.ConfigRepo, credService credential.Service, credRepo *store.CredentialRepo, auth *rbac) {
	providers := g.Group("/providers", auth.auditMutation("provider", resourceIDFrom))
	providers.POST("", func(c *gin.Context) {
		var req providerRequest
		if !bind(c, &req) {
			return
		}
		if req.Provider.Name == "" {
			badRequest(c, "provider name is required")
			return
		}
		// Multi-endpoint validation (ADR-0049): at least one endpoint, known
		// adapters, non-empty base_urls, unique endpoint ids (adapter-derived
		// defaults openai/anthropic when id is omitted).
		if err := config.ValidateProvider(&req.Provider); err != nil {
			badRequest(c, err.Error())
			return
		}
		req.Provider.APIKeyRef = effectiveAPIKeyRef(req.Provider.Name, req.APIKey, req.Provider.APIKeyRef)
		if err := repo.UpsertProvider(c.Request.Context(), req.Provider); err != nil {
			internalErr(c, err)
			return
		}
		if err := persistProviderCredential(c.Request.Context(), req.Provider.Name, req.APIKey, req.Provider.APIKeyRef, credService, credRepo); err != nil {
			appErrMsg(c, apperr.ProviderCreateFailed, err.Error())
			return
		}
		setResourceID(c, req.Provider.Name)
		c.JSON(http.StatusCreated, maskedProvider(req.Provider))
	})
	providers.GET("", func(c *gin.Context) {
		list, err := repo.ListProviders(c.Request.Context())
		if err != nil {
			internalErr(c, err)
			return
		}
		masked := make([]config.Provider, len(list))
		for i, p := range list {
			masked[i] = maskedProvider(p)
		}
		c.JSON(http.StatusOK, listEnvelope(masked, ""))
	})
	providers.PATCH("/:name", func(c *gin.Context) {
		name := c.Param("name")
		var patch store.ProviderPatch
		if !bind(c, &patch) {
			return
		}
		// Endpoints, if provided, are a whole-list replacement and must
		// themselves validate (ADR-0049): the patched provider is checked by
		// PatchProvider's read-modify-write, so validate the merged result.
		if patch.Endpoints != nil {
			merged := config.Provider{Name: name, Endpoints: *patch.Endpoints}
			if err := config.ValidateProvider(&merged); err != nil {
				badRequest(c, err.Error())
				return
			}
		}
		updated, ok, err := repo.PatchProvider(c.Request.Context(), name, patch)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ProviderNotFound)
			return
		}
		setResourceID(c, name)
		c.JSON(http.StatusOK, updated)
	})
	providers.DELETE("/:name", func(c *gin.Context) {
		name := c.Param("name")
		// Check for references before deleting (ADR-0019: reject deletion that
		// would cause dangling provider references at runtime → 503).
		refModels, refRoutes, err := repo.ProviderReferencedBy(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if len(refModels) > 0 || len(refRoutes) > 0 {
			msg := "provider is referenced"
			if len(refModels) > 0 {
				msg += " by model(s) " + strings.Join(refModels, ", ")
			}
			if len(refRoutes) > 0 {
				msg += " by route(s) " + strings.Join(refRoutes, ", ")
			}
			msg += "; delete or repoint them first"
			appErrMsg(c, apperr.ProviderDeleteFailed, msg)
			return
		}
		if err := repo.DeleteProvider(c.Request.Context(), name); err != nil {
			internalErr(c, err)
			return
		}
		// Cascade handles provider_credentials row; explicit delete for safety.
		if credRepo != nil {
			_ = credRepo.Delete(c.Request.Context(), name)
		}
		setResourceID(c, name)
		c.Status(http.StatusNoContent)
	})

	// Dedicated credential update endpoint. Accepts a plaintext api_key or an
	// external api_key_ref (env://, etc.). The response only confirms the stored
	// reference, never the secret.
	providers.PATCH("/:name/credential", func(c *gin.Context) {
		name := c.Param("name")
		var req struct {
			APIKey    string `json:"api_key,omitempty"`
			APIKeyRef string `json:"api_key_ref,omitempty"`
		}
		if !bind(c, &req) {
			return
		}
		if req.APIKey == "" && req.APIKeyRef == "" {
			badRequest(c, "api_key or api_key_ref is required")
			return
		}
		if err := persistProviderCredential(c.Request.Context(), name, req.APIKey, req.APIKeyRef, credService, credRepo); err != nil {
			appErrMsg(c, apperr.ProviderUpdateFailed, err.Error())
			return
		}
		ref := effectiveAPIKeyRef(name, req.APIKey, req.APIKeyRef)
		// Update the provider's api_key_ref pointer to match the stored credential.
		p, ok, err := repo.GetProvider(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ProviderNotFound)
			return
		}
		p.APIKeyRef = ref
		if err := repo.UpsertProvider(c.Request.Context(), p); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, name)
		c.JSON(http.StatusOK, gin.H{"api_key_ref": maskAPIKeyRef(ref)})
	})
}

// effectiveAPIKeyRef returns the api_key_ref that should be persisted. If a
// plaintext api_key was supplied, it is replaced by the db:// reference. If
// only api_key_ref was supplied, that reference is kept. If neither was
// supplied, the existing ref is preserved.
func effectiveAPIKeyRef(providerName, apiKey, apiKeyRef string) string {
	if apiKey != "" {
		return config.DBProviderRef(providerName)
	}
	return apiKeyRef
}

// persistProviderCredential encrypts and stores the credential when a plaintext
// api_key is provided. When only an api_key_ref is provided, no credential table
// operation is performed. If neither is provided, any existing credential is
// left untouched unless the caller explicitly deleted it (deletion is handled
// by the DELETE endpoint).
func persistProviderCredential(ctx context.Context, providerName, apiKey, apiKeyRef string, credService credential.Service, credRepo *store.CredentialRepo) error {
	if apiKey == "" {
		return nil
	}
	if credService == nil {
		return fmt.Errorf("credential encryption service not configured")
	}
	if credRepo == nil {
		return fmt.Errorf("credential repository not configured")
	}
	cred, err := credService.Encrypt(apiKey)
	if err != nil {
		return fmt.Errorf("encrypt credential: %w", err)
	}
	cred.ProviderName = providerName
	if err := credRepo.Upsert(ctx, providerName, cred); err != nil {
		return fmt.Errorf("store credential: %w", err)
	}
	return nil
}

func isKnownAdapter(name string) bool {
	for _, n := range adapter.Registered() {
		if n == name {
			return true
		}
	}
	return false
}
