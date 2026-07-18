//go:build dbtest

package app_test

import (
	"testing"
	"time"

	"voxeltoad/internal/app"
	"voxeltoad/internal/billing"
)

// seed inserts a tenant, group, and api_key directly so KeyStore lookups resolve.
func seed(t *testing.T, stores *app.Stores, keyID, hash, tenant, group string) {
	t.Helper()
	db := stores.DB()
	var tenantID, groupID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES (?) RETURNING id`, tenant).Scan(&tenantID).Error; err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Raw(`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, group).Scan(&groupID).Error; err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, allowed_models)
		 VALUES (?, ?, ?, ?, '[]'::jsonb)`,
		keyID, hash, tenantID, groupID,
	).Error; err != nil {
		t.Fatalf("insert api_key: %v", err)
	}
}

func billingUsage() billing.UsageRecord {
	return billing.UsageRecord{
		Tenant: "acme-app", Group: "team-app", APIKeyID: "key_app",
		Provider: "openai", Model: "gpt-4o",
		PromptTokens: 10, CompletionTokens: 5, Cost: 1000,
	}
}

func countUsage(t *testing.T, stores *app.Stores) int {
	t.Helper()
	var n int
	if err := stores.DB().Raw(`SELECT count(*) FROM usage_records`).Scan(&n).Error; err != nil {
		t.Fatalf("count usage: %v", err)
	}
	return n
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}
