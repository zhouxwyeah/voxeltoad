package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/config"
	"voxeltoad/internal/store"
)

// mountPluginCRUD wires plugin CRUD. Phase must be one of pre/post.
func mountPluginCRUD(g *gin.RouterGroup, repo *store.ConfigRepo, auth *rbac) {
	plugins := g.Group("/plugins", auth.auditMutation("plugin", resourceIDFrom))
	plugins.POST("", func(c *gin.Context) {
		var pc config.PluginConfig
		if !bind(c, &pc) {
			return
		}
		if pc.Name == "" {
			badRequest(c, "plugin name is required")
			return
		}
		switch pc.Phase {
		case "pre", "post":
			// ok
		default:
			badRequest(c, "plugin phase must be one of pre, post")
			return
		}
		if err := repo.UpsertPlugin(c.Request.Context(), pc); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, pc.Name)
		c.JSON(http.StatusCreated, pc)
	})
	plugins.GET("", func(c *gin.Context) {
		cursor := c.Query("cursor")
		limit, _ := strconv.Atoi(c.Query("limit"))
		list, nextCursor, err := repo.ListPluginsPaged(c.Request.Context(), cursor, limit)
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, listEnvelope(list, nextCursor))
	})
	plugins.GET("/:name", func(c *gin.Context) {
		pc, ok, err := repo.GetPlugin(c.Request.Context(), c.Param("name"), c.Query("scope"))
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.PluginNotFound)
			return
		}
		c.JSON(http.StatusOK, pc)
	})
	plugins.PATCH("/:name", func(c *gin.Context) {
		name := c.Param("name")
		scope := c.Query("scope")
		var patch store.PluginPatch
		if !bind(c, &patch) {
			return
		}
		if patch.Phase != nil {
			switch *patch.Phase {
			case "pre", "post":
				// ok
			default:
				badRequest(c, "plugin phase must be one of pre, post")
				return
			}
		}
		updated, ok, err := repo.PatchPlugin(c.Request.Context(), name, scope, patch)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.PluginNotFound)
			return
		}
		setResourceID(c, name)
		c.JSON(http.StatusOK, updated)
	})
	plugins.DELETE("/:name", func(c *gin.Context) {
		if err := repo.DeletePlugin(c.Request.Context(), c.Param("name"), c.Query("scope")); err != nil {
			internalErr(c, err)
			return
		}
		setResourceID(c, c.Param("name"))
		c.Status(http.StatusNoContent)
	})
}
