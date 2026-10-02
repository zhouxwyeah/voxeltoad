//go:build dbtest

package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"voxeltoad/internal/store"
)

func TestEnterpriseAPIKey_CreateIdentityValidation(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, ownerID, appID := seedApplication(t, db, "enterprise-a", "owner", "application")
	_, foreignGroup, foreignApp := seedApplication(t, db, "enterprise-b", "owner", "application")
	repo := store.NewTenantRepo(db, tenantID)
	consumerID, err := repo.CreateGroup(ctx, "consumer")
	if err != nil {
		t.Fatal(err)
	}
	disabledGroup, err := repo.CreateGroup(ctx, "disabled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetGroupEnabled(ctx, "disabled", false); err != nil {
		t.Fatal(err)
	}
	apps := store.NewApplicationRepo(db, tenantID)
	disabledApp, err := apps.Create(ctx, "disabled", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apps.SetEnabled(ctx, "disabled", false); err != nil {
		t.Fatal(err)
	}
	missing := int64(999999)
	zero := int64(0)
	for index, tc := range []struct {
		name       string
		group, app *int64
		env        string
		valid      bool
	}{
		{"consumer differs from owner", &consumerID, &appID, "prod", true},
		{"dev", &ownerID, &appID, "dev", true},
		{"staging", &ownerID, &appID, "staging", true},
		{"missing group", nil, &appID, "prod", false},
		{"missing application", &consumerID, nil, "prod", false},
		{"missing environment", &consumerID, &appID, "", false},
		{"invalid environment", &consumerID, &appID, "production", false},
		{"zero group", &zero, &appID, "prod", false},
		{"unknown group", &missing, &appID, "prod", false},
		{"unknown application", &consumerID, &missing, "prod", false},
		{"cross tenant group", &foreignGroup, &appID, "prod", false},
		{"cross tenant application", &consumerID, &foreignApp, "prod", false},
		{"disabled group", &disabledGroup, &appID, "prod", false},
		{"disabled application", &consumerID, &disabledApp, "prod", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyID := fmt.Sprintf("enterprise-%d", index)
			err := repo.CreateEnterpriseAPIKey(ctx, store.APIKeySpec{KeyID: keyID, Hash: keyID,
				GroupID: tc.group, ApplicationID: tc.app, Environment: tc.env})
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, store.ErrInvalidAPIKeyIdentity) {
				t.Fatalf("err = %v, want invalid identity", err)
			}
			var count int64
			if err := db.Raw(`SELECT count(*) FROM api_keys WHERE key_id = ?`, keyID).Scan(&count).Error; err != nil {
				t.Fatal(err)
			}
			if (count == 1) != tc.valid {
				t.Fatalf("persisted count=%d, valid=%v", count, tc.valid)
			}
		})
	}
	if _, err := store.SetTenantEnabled(ctx, db, "enterprise-a", false); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateEnterpriseAPIKey(ctx, store.APIKeySpec{KeyID: "disabled-tenant", Hash: "disabled-tenant",
		GroupID: &consumerID, ApplicationID: &appID, Environment: "prod"}); !errors.Is(err, store.ErrInvalidAPIKeyIdentity) {
		t.Fatalf("disabled tenant accepted: %v", err)
	}
}

func TestEnterpriseAPIKey_CompletionAtomicAndImmutable(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, appID := seedApplication(t, db, "completion", "owner", "app")
	foreignTenant, foreignGroup, foreignApp := seedApplication(t, db, "foreign-completion", "owner", "app")
	repo := store.NewTenantRepo(db, tenantID)
	identity := store.APIKeyIdentity{GroupID: groupID, ApplicationID: appID, Environment: "prod"}
	for _, tc := range []struct {
		name       string
		group, app *int64
		env        string
	}{
		{"none", nil, nil, ""},
		{"group", &groupID, nil, ""},
		{"application", nil, &appID, ""},
		{"environment", nil, nil, "prod"},
		{"no-group", nil, &appID, "prod"},
		{"no-application", &groupID, nil, "prod"},
		{"no-environment", &groupID, &appID, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.CreateAPIKey(ctx, store.APIKeySpec{KeyID: tc.name, Hash: tc.name,
				GroupID: tc.group, ApplicationID: tc.app, Environment: tc.env}); err != nil {
				t.Fatal(err)
			}
			models := []string{"model-a"}
			if ok, err := repo.UpdateAPIKey(ctx, tc.name, store.APIKeyUpdate{Identity: &identity, AllowedModels: &models}); !ok || err != nil {
				t.Fatalf("complete: ok=%v err=%v", ok, err)
			}
			for _, changed := range []store.APIKeyIdentity{
				{GroupID: foreignGroup, ApplicationID: appID, Environment: "prod"},
				{GroupID: groupID, ApplicationID: foreignApp, Environment: "prod"},
				{GroupID: groupID, ApplicationID: appID, Environment: "dev"},
			} {
				otherModels := []string{"model-b"}
				if ok, err := repo.UpdateAPIKey(ctx, tc.name, store.APIKeyUpdate{Identity: &changed, AllowedModels: &otherModels}); ok || !errors.Is(err, store.ErrAPIKeyIdentityImmutable) {
					t.Fatalf("changed identity: ok=%v err=%v", ok, err)
				}
			}
		})
	}
	keys, _, err := repo.ListAPIKeys(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if key.GroupID == nil || *key.GroupID != groupID || key.ApplicationID == nil || *key.ApplicationID != appID || key.Environment != "prod" || len(key.AllowedModels) != 1 || key.AllowedModels[0] != "model-a" {
			t.Errorf("unexpected key after completion or rejected update: %+v", key)
		}
	}
	// Existing legacy components cannot be overwritten while completing others.
	if err := repo.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "fixed-env", Hash: "fixed-env", Environment: "dev"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.UpdateAPIKey(ctx, "fixed-env", store.APIKeyUpdate{Identity: &identity}); ok || !errors.Is(err, store.ErrAPIKeyIdentityImmutable) {
		t.Fatalf("legacy environment changed: ok=%v err=%v", ok, err)
	}
	if ok, err := store.NewTenantRepo(db, foreignTenant).UpdateAPIKey(ctx, "none", store.APIKeyUpdate{Identity: &identity}); ok || err != nil {
		t.Fatalf("cross tenant key visible: ok=%v err=%v", ok, err)
	}
	if _, err := repo.RevokeAPIKey(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.UpdateAPIKey(ctx, "none", store.APIKeyUpdate{Identity: &identity}); ok || err != nil {
		t.Fatalf("revoked key modified: ok=%v err=%v", ok, err)
	}
}

func TestEnterpriseAPIKey_UnboundPagination(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, appID := seedApplication(t, db, "unbound-pages", "owner", "app")
	repo := store.NewTenantRepo(db, tenantID)
	for _, spec := range []store.APIKeySpec{
		{KeyID: "bound", GroupID: &groupID, ApplicationID: &appID, Environment: "prod"},
		{KeyID: "no-group", ApplicationID: &appID, Environment: "prod"},
		{KeyID: "no-app", GroupID: &groupID, Environment: "prod"},
		{KeyID: "no-env", GroupID: &groupID, ApplicationID: &appID},
		{KeyID: "revoked"},
	} {
		spec.Hash = spec.KeyID
		if err := repo.CreateAPIKey(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.RevokeAPIKey(ctx, "revoked"); err != nil {
		t.Fatal(err)
	}
	first, next, err := repo.ListAPIKeysFiltered(ctx, "", 2, true)
	if err != nil || len(first) != 2 || next == "" || first[0].KeyID != "no-group" || first[1].KeyID != "no-app" {
		t.Fatalf("first=%+v next=%q err=%v", first, next, err)
	}
	second, next, err := repo.ListAPIKeysFiltered(ctx, next, 2, true)
	if err != nil || len(second) != 1 || next != "" || second[0].KeyID != "no-env" {
		t.Fatalf("second=%+v next=%q err=%v", second, next, err)
	}
}

func TestEnterpriseAPIKey_ConcurrentCompletion(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, appID := seedApplication(t, db, "concurrent-binding", "owner", "app")
	repo := store.NewTenantRepo(db, tenantID)
	if err := repo.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "racing-key", Hash: "racing-key"}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, env := range []string{"dev", "prod"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := repo.UpdateAPIKey(ctx, "racing-key", store.APIKeyUpdate{Identity: &store.APIKeyIdentity{
				GroupID: groupID, ApplicationID: appID, Environment: env,
			}})
			if err == nil && !ok {
				err = fmt.Errorf("key not found")
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, store.ErrAPIKeyIdentityImmutable) {
			rejected++
		} else {
			t.Errorf("unexpected completion error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("succeeded=%d rejected=%d, want one each", succeeded, rejected)
	}
}
