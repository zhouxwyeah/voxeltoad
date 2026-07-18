package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/store"
)

// mountDataPlaneNodes wires the data-plane node list endpoint (super-admin
// only — mounted under globalGrp). The data plane self-registers via heartbeat
// in cmd/gateway/main.go.
func mountDataPlaneNodes(g *gin.RouterGroup, dpRepo *store.DataPlaneRepo) {
	if dpRepo == nil {
		return
	}
	g.GET("/data-plane-nodes", func(c *gin.Context) {
		nodes, err := dpRepo.List(c.Request.Context())
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(nodes, ""))
	})
}
