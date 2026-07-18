package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// mountGatewaySettings wires the gateway-wide behavior settings (trace capture,
// future otel/rate_limit). Super-admin only (mounted under globalGrp). The
// single settings document is read by the data plane via the config snapshot
// (Dynamic.Settings) and applied per-request, hot-reloadable.
//
// PUT is audited as resource_type "gateway_settings" (a global resource; the
// affected-tenant resolver returns nil for it). GET is not audited (settings
// values are non-secret — they are knobs, not prompt bodies).
func mountGatewaySettings(g *gin.RouterGroup, repo *store.ConfigRepo, auth *rbac) {
	s := g.Group("/gateway-settings", auth.auditMutation("gateway_settings", resourceIDFrom))

	// GET /api/v1/gateway-settings — the current settings document.
	s.GET("", func(c *gin.Context) {
		settings, err := repo.GetSettings(c.Request.Context())
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, settings)
	})

	// PUT /api/v1/gateway-settings — replace the settings document. Bumps
	// config_generation so the data plane picks up the change on its next poll.
	s.PUT("", func(c *gin.Context) {
		var in config.GatewaySettings
		if !bind(c, &in) {
			return
		}
		// Validate trace retention: <=0 is allowed (means "use default"); cap
		// negative MaxBodyKB to 0 (uncapped) for safety.
		if in.Trace.MaxBodyKB < 0 {
			in.Trace.MaxBodyKB = 0
		}
		if err := repo.UpdateSettings(c.Request.Context(), &in); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, "default") // single document; stable id for the audit row
		c.JSON(http.StatusOK, in)
	})
}
