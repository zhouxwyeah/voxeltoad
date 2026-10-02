//go:build dbtest

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

func enterpriseKeyBody(t *testing.T, h http.Handler, token, keyID string) map[string]any {
	t.Helper()
	owner := keyID + "-owner"
	rr := doAuth(t, h, token, http.MethodPost, "/api/v1/groups", map[string]any{"name": owner})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create owner: %d %s", rr.Code, rr.Body.String())
	}
	var group store.Group
	if err := json.Unmarshal(rr.Body.Bytes(), &group); err != nil {
		t.Fatal(err)
	}
	rr = doAuth(t, h, token, http.MethodPost, "/api/v1/applications", map[string]any{"name": keyID + "-app", "owner_group": owner})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create application: %d %s", rr.Code, rr.Body.String())
	}
	var app store.Application
	if err := json.Unmarshal(rr.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"key_id": keyID, "group_id": group.ID, "application_id": app.ID, "environment": "prod"}
}

func TestAPIKey_EnterpriseIdentityRequired(t *testing.T) {
	h, db, token := seededTenantAdmin(t)
	body := enterpriseKeyBody(t, h, token, "required")
	for _, field := range []string{"group_id", "application_id", "environment"} {
		t.Run(field, func(t *testing.T) {
			value := body[field]
			delete(body, field)
			rr := doAuth(t, h, token, http.MethodPost, "/api/v1/api-keys", body)
			body[field] = value
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("missing field: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
	for _, env := range []any{nil, "", "production"} {
		body["environment"] = env
		if rr := doAuth(t, h, token, http.MethodPost, "/api/v1/api-keys", body); rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid environment: %d %s", rr.Code, rr.Body.String())
		}
	}
	var count int64
	if err := db.Raw(`SELECT count(*) FROM api_keys WHERE key_id = 'required'`).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid keys persisted: count=%d err=%v", count, err)
	}
}

func TestAPIKey_EnterpriseIdentityConsumerAndCrossTenant(t *testing.T) {
	h, db, token := seededTenantAdmin(t)
	body := enterpriseKeyBody(t, h, token, "consumer")
	var tenantID int64
	if err := db.Raw(`SELECT tenant_id FROM groups WHERE id = ?`, body["group_id"]).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	consumerID, err := store.NewTenantRepo(db, tenantID).CreateGroup(context.Background(), "other-consumer")
	if err != nil {
		t.Fatal(err)
	}
	body["group_id"] = consumerID
	rr := doAuth(t, h, token, http.MethodPost, "/api/v1/api-keys", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("cross-group consumer rejected: %d %s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["group_id"] != float64(consumerID) || created["application_id"] != float64(body["application_id"].(int64)) || created["environment"] != "prod" || created["api_key"] == "" {
		t.Fatalf("incomplete create identity: %v", created)
	}
	foreignTenant, err := store.CreateTenant(context.Background(), db, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	foreignGroup, err := store.NewTenantRepo(db, foreignTenant).CreateGroup(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	foreignApp, err := store.NewApplicationRepo(db, foreignTenant).Create(context.Background(), "app", "owner")
	if err != nil {
		t.Fatal(err)
	}
	body["key_id"] = "foreign-key"
	for _, tc := range []struct {
		field string
		value int64
	}{{"group_id", foreignGroup}, {"application_id", foreignApp}} {
		previous := body[tc.field]
		body[tc.field] = tc.value
		if rr := doAuth(t, h, token, http.MethodPost, "/api/v1/api-keys", body); rr.Code != http.StatusBadRequest {
			t.Fatalf("cross-tenant %s accepted: %d %s", tc.field, rr.Code, rr.Body.String())
		}
		body[tc.field] = previous
	}
}

func TestAPIKey_LegacyCompletionAndUnboundList(t *testing.T) {
	ctx := context.Background()
	h, db, token := seededTenantAdmin(t)
	body := enterpriseKeyBody(t, h, token, "legacy")
	var tenantID int64
	if err := db.Raw(`SELECT tenant_id FROM groups WHERE id = ?`, body["group_id"]).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	repo := store.NewTenantRepo(db, tenantID)
	groupID := body["group_id"].(int64)
	appID := body["application_id"].(int64)
	for _, spec := range []store.APIKeySpec{
		{KeyID: "legacy", Hash: "legacy"},
		{KeyID: "no-group", Hash: "no-group", ApplicationID: &appID, Environment: "prod"},
		{KeyID: "no-env", Hash: "no-env", GroupID: &groupID, ApplicationID: &appID},
	} {
		if err := repo.CreateAPIKey(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	rr := doAuth(t, h, token, http.MethodGet, "/api/v1/api-keys?unbound=true&limit=2", nil)
	var first struct {
		Data []store.APIKeyInfo `json:"data"`
		Next string             `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil || rr.Code != http.StatusOK || len(first.Data) != 2 || first.Next == "" {
		t.Fatalf("first unbound page: %d %s err=%v", rr.Code, rr.Body.String(), err)
	}
	if first.Data[0].GroupID != nil || first.Data[0].ApplicationID != nil || first.Data[0].Environment != "" {
		t.Fatalf("legacy identity guessed: %+v", first.Data[0])
	}
	rr = doAuth(t, h, token, http.MethodGet, "/api/v1/api-keys?unbound=true&limit=2&cursor="+first.Next, nil)
	if rows := decodeList(t, rr); len(rows) != 1 || rows[0]["key_id"] != "no-env" {
		t.Fatalf("second unbound page: %v", rows)
	}
	for _, patch := range []map[string]any{
		{"application_id": appID},
		{"group_id": nil, "application_id": appID, "environment": "prod"},
		{"group_id": groupID, "application_id": nil, "environment": "prod"},
		{"group_id": groupID, "application_id": appID, "environment": nil},
	} {
		if rr := doAuth(t, h, token, http.MethodPatch, "/api/v1/api-keys/legacy", patch); rr.Code != http.StatusBadRequest {
			t.Fatalf("partial identity accepted: %d %s", rr.Code, rr.Body.String())
		}
	}
	if err := db.Exec(`INSERT INTO models (alias, spec) VALUES ('first', '{}'), ('second', '{}')`).Error; err != nil {
		t.Fatal(err)
	}
	body["allowed_models"] = []string{"first"}
	for range 2 {
		if rr := doAuth(t, h, token, http.MethodPatch, "/api/v1/api-keys/legacy", body); rr.Code != http.StatusNoContent {
			t.Fatalf("identity completion/retry: %d %s", rr.Code, rr.Body.String())
		}
	}
	body["environment"] = "dev"
	body["allowed_models"] = []string{"second"}
	if rr := doAuth(t, h, token, http.MethodPatch, "/api/v1/api-keys/legacy", body); rr.Code != http.StatusBadRequest {
		t.Fatalf("identity rewrite accepted: %d %s", rr.Code, rr.Body.String())
	}
	keys, _, err := repo.ListAPIKeys(ctx, "", 50)
	if err != nil || len(keys) != 3 || keys[0].Environment != "prod" || len(keys[0].AllowedModels) != 1 || keys[0].AllowedModels[0] != "first" {
		t.Fatalf("rejected update leaked: %+v err=%v", keys, err)
	}
	if rr := doAuth(t, h, token, http.MethodPatch, "/api/v1/api-keys/legacy", map[string]any{"allowed_models": []string{"second"}}); rr.Code != http.StatusNoContent {
		t.Fatalf("model-only update rejected: %d %s", rr.Code, rr.Body.String())
	}
	rr = doAuth(t, h, token, http.MethodGet, "/api/v1/api-keys?unbound=true", nil)
	if rows := decodeList(t, rr); len(rows) != 2 {
		t.Fatalf("completed key still unbound: %v", rows)
	}
}

func TestIdentityReferences_GroupDeletion(t *testing.T) {
	h, _, token := seededTenantAdmin(t)
	body := enterpriseKeyBody(t, h, token, "reference")
	// The application alone protects its owner group, even without any key.
	if rr := doAuth(t, h, token, http.MethodDelete, "/api/v1/groups/reference-owner", nil); rr.Code != http.StatusConflict {
		t.Fatalf("application owner deleted: %d %s", rr.Code, rr.Body.String())
	}
	rr := doAuth(t, h, token, http.MethodPost, "/api/v1/groups", map[string]any{"name": "consumer"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", rr.Code, rr.Body.String())
	}
	var group store.Group
	if err := json.Unmarshal(rr.Body.Bytes(), &group); err != nil {
		t.Fatal(err)
	}
	body["group_id"] = group.ID
	if rr := doAuth(t, h, token, http.MethodPost, "/api/v1/api-keys", body); rr.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doAuth(t, h, token, http.MethodDelete, "/api/v1/api-keys/reference", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("revoke key: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doAuth(t, h, token, http.MethodDelete, "/api/v1/groups/consumer", nil); rr.Code != http.StatusConflict {
		t.Fatalf("retained key group deletion must return409: %d %s", rr.Code, rr.Body.String())
	}
}

func TestIdentityPermissions_ReadOnlyCannotWrite(t *testing.T) {
	ctx := context.Background()
	h, db, token := seededTenantAdmin(t)
	body := enterpriseKeyBody(t, h, token, "protected")
	if rr := doAuth(t, h, token, http.MethodPost, "/api/v1/api-keys", body); rr.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", rr.Code, rr.Body.String())
	}
	var tenantID int64
	if err := db.Raw(`SELECT tenant_id FROM groups WHERE id = ?`, body["group_id"]).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		permissions []string
		readable    []string
	}{
		{"identity-reader", []string{"api_key.read", "application.read", "group.read"}, []string{"api-keys", "applications", "groups"}},
		{"application-reader", []string{"application.read"}, []string{"applications"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role := &store.Role{Name: tc.name, ScopeKind: "tenant"}
			if err := store.NewRoleRepo(db).Create(ctx, role, tc.permissions); err != nil {
				t.Fatal(err)
			}
			hash, err := operator.HashPassword("read-only-pass")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.NewOperatorRepo(db).Create(ctx, tc.name+"@test", hash, operator.Role(tc.name), &tenantID); err != nil {
				t.Fatal(err)
			}
			readToken := login(t, h, tc.name+"@test", "read-only-pass")
			for _, path := range tc.readable {
				if rr := doAuth(t, h, readToken, http.MethodGet, "/api/v1/"+path, nil); rr.Code != http.StatusOK {
					t.Fatalf("authorized read rejected: %d %s", rr.Code, rr.Body.String())
				}
			}
			for _, request := range []struct{ method, path string }{
				{http.MethodPost, "/api-keys"}, {http.MethodPatch, "/api-keys/protected"}, {http.MethodDelete, "/api-keys/protected"},
				{http.MethodPost, "/applications"}, {http.MethodPatch, "/applications/protected-app"}, {http.MethodDelete, "/applications/protected-app"},
				{http.MethodPost, "/groups"}, {http.MethodPatch, "/groups/protected-owner"}, {http.MethodDelete, "/groups/protected-owner"},
			} {
				rr := doAuth(t, h, readToken, request.method, "/api/v1"+request.path, map[string]any{"enabled": false})
				if rr.Code != http.StatusForbidden {
					t.Errorf("%s %s = %d, want403: %s", request.method, request.path, rr.Code, rr.Body.String())
				}
			}
			if tc.name == "application-reader" {
				if rr := doAuth(t, h, readToken, http.MethodGet, "/api/v1/api-keys", nil); rr.Code != http.StatusForbidden {
					t.Fatalf("application read granted key access: %d %s", rr.Code, rr.Body.String())
				}
			}
		})
	}
}
