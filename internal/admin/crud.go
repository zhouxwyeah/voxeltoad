package admin

import (
	"context"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/store"
)

// auditResourceIDKey is where a handler stashes the affected resource id for the
// audit middleware to read.
const auditResourceIDKey = "emg.resource_id"

// setResourceID records the id the audit row should reference.
func setResourceID(c *gin.Context, id string) { c.Set(auditResourceIDKey, id) }

func resourceIDFrom(c *gin.Context) string {
	if v, ok := c.Get(auditResourceIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// mountConfigCRUD wires REST CRUD for the platform-level config resources over
// the ConfigRepo. Each resource type lives in its own file so parallel worktrees
// working on different domains don't collide on a single crud.go:
//   - crud_provider.go  (providers)
//   - crud_model.go     (models)
//   - crud_route.go     (routes)
//   - crud_plugin.go    (plugins)
//
// Cross-references (route→provider, model upstream→provider) are validated
// against existing providers at write time (ADR-0014) → 400. Every mutation is
// audited via middleware (ADR-0017 §5).
func mountConfigCRUD(readGroup *gin.RouterGroup, writeGroup *gin.RouterGroup, repo *store.ConfigRepo, credService credential.Service, credRepo *store.CredentialRepo, auth *rbac) {
	mountProviderCRUD(writeGroup, repo, credService, credRepo, auth)
	mountProviderConnTest(writeGroup, repo, credService, credRepo)
	mountModelCRUD(readGroup, writeGroup, repo, auth)
	mountRouteCRUD(writeGroup, repo, auth)
	mountPluginCRUD(writeGroup, repo, auth)
}

// providerSet returns the set of existing provider names, for cross-reference
// validation.
func providerSet(ctx context.Context, repo *store.ConfigRepo) (map[string]bool, error) {
	providers, err := repo.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(providers))
	for _, p := range providers {
		set[p.Name] = true
	}
	return set, nil
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		badRequest(c, "invalid request body: "+err.Error())
		return false
	}
	return true
}

func badRequest(c *gin.Context, msg string) {
	appErrMsg(c, apperr.InvalidBody, msg)
}

func internalErr(c *gin.Context, err error) {
	appErrMsg(c, apperr.Unexpected, err.Error())
}
