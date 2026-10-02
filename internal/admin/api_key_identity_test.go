package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/authz"
	"voxeltoad/internal/operator"
)

func TestIdentityPermissionGuards(t *testing.T) {
	tenantID := int64(1)
	for _, tc := range []struct {
		name        string
		tenant      *int64
		permissions map[string]bool
		required    authz.Permission
		want        int
	}{
		{"key reader reads", &tenantID, map[string]bool{"api_key.read": true}, authz.PermAPIKeyRead, http.StatusNoContent},
		{"key reader cannot write", &tenantID, map[string]bool{"api_key.read": true}, authz.PermAPIKeyWrite, http.StatusForbidden},
		{"key writer need not read", &tenantID, map[string]bool{"api_key.write": true}, authz.PermAPIKeyWrite, http.StatusNoContent},
		{"application reader reads", &tenantID, map[string]bool{"application.read": true}, authz.PermApplicationRead, http.StatusNoContent},
		{"application reader cannot write", &tenantID, map[string]bool{"application.read": true}, authz.PermApplicationWrite, http.StatusForbidden},
		{"application reader cannot read keys", &tenantID, map[string]bool{"application.read": true}, authz.PermAPIKeyRead, http.StatusForbidden},
		{"application writer need not read", &tenantID, map[string]bool{"application.write": true}, authz.PermApplicationWrite, http.StatusNoContent},
		{"no tenant", nil, map[string]bool{"api_key.write": true}, authz.PermAPIKeyWrite, http.StatusForbidden},
		{"global wildcard", nil, map[string]bool{"*": true}, authz.PermAPIKeyWrite, http.StatusForbidden},
		{"tenant alone insufficient", &tenantID, nil, authz.PermAPIKeyRead, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &rbac{}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(operatorCtxKey, operator.Operator{TenantID: tc.tenant, Permissions: tc.permissions})
			})
			router.GET("/test", auth.requireTenantAdmin(), auth.requirePermission(tc.required), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/test", nil))
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestIdentityRoutes_ReadOnlyCannotReachMutations(t *testing.T) {
	tenantID := int64(1)
	auth := &rbac{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(operatorCtxKey, operator.Operator{TenantID: &tenantID, Permissions: map[string]bool{
			"api_key.read": true, "application.read": true, "group.read": true,
		}})
	})
	group := router.Group("/api/v1", auth.requireTenantAdmin())
	// No database: any missed guard would reach the mutation and panic.
	mountTenantScoped(group, nil, auth)
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api-keys"}, {http.MethodPatch, "/api-keys/key"}, {http.MethodDelete, "/api-keys/key"},
		{http.MethodPost, "/applications"}, {http.MethodPatch, "/applications/app"}, {http.MethodDelete, "/applications/app"},
		{http.MethodPost, "/groups"}, {http.MethodPatch, "/groups/group"}, {http.MethodDelete, "/groups/group"},
	} {
		t.Run(request.method+request.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(request.method, "/api/v1"+request.path, nil))
			if rr.Code != http.StatusForbidden {
				t.Fatalf("unguarded route: status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}
