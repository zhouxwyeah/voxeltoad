//go:build dbtest

package admin_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"voxeltoad/internal/store"
)

func TestLedgerAttribution_InvalidFilters(t *testing.T) {
	h, _, tok := authedAdmin(t)
	for _, endpoint := range []string{"/usage", "/usage/summary", "/usage/timeseries", "/request-logs"} {
		for _, query := range []string{
			"application_id=0", "application_id=-1", "application_id=abc", "application_id=9223372036854775808", "application_id=", "application_id=1&application_id=2",
			"application_id=" + url.QueryEscape("1 OR 1=1"),
			"environment=qa", "environment=", "environment=prod&environment=dev",
			"unattributed=1", "unattributed=", "unattributed=invalid", "unattributed=true&application_id=1",
		} {
			t.Run(endpoint+"?"+query, func(t *testing.T) {
				rr := doAuth(t, h, tok, http.MethodGet, "/api/v1"+endpoint+"?"+query, nil)
				if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"type":"invalid_body"`) {
					t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
				}
			})
		}
	}
}

func TestUsage_AttributionCurrenciesAndCSV(t *testing.T) {
	h, db, _ := authedAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	tok := login(t, h, "ta@acme", "ta-pass-123")
	if err := db.Exec(`INSERT INTO usage_records (tenant, api_key_id, provider, model, application_id, environment, currency, cost) VALUES
		('acme', 'k', '=UPSTREAM()', 'chat', 901, 'prod', 'USD', 100),
		('acme', 'k', 'openai', 'chat', 901, 'prod', 'CNY', 200),
		('acme', 'k', 'openai', 'chat', 901, 'dev', 'USD', 300),
		('acme', 'k', 'openai', 'chat', NULL, '', '', 50),
		('other', 'k', 'openai', 'chat', 901, 'prod', 'USD', 9999),
		('other', 'k', 'openai', 'chat', NULL, '', '', 8888)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/usage", "/usage/summary?group_by=application_id", "/usage/timeseries?bucket=day"} {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		rr := doAuth(t, h, tok, http.MethodGet, "/api/v1"+endpoint+separator+"application_id=901&environment=prod&tenant=other", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", endpoint, rr.Code, rr.Body.String())
		}
		rows := decodeList(t, rr)
		if len(rows) != 2 {
			t.Fatalf("%s rows = %+v", endpoint, rows)
		}
		for _, row := range rows {
			want := map[string]float64{"USD": 100, "CNY": 200}[row["currency"].(string)]
			if row["cost"] != want {
				t.Fatalf("%s currencies mixed: %+v", endpoint, row)
			}
			if endpoint == "/usage" && (row["application_id"] != float64(901) || row["environment"] != "prod" || row["tenant"] != "acme") {
				t.Fatalf("lost attribution: %+v", row)
			}
		}
	}
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary?group_by=application_id&unattributed=true", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("legacy summary: %d %s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 || rows[0]["group_key"] != "unattributed" || rows[0]["currency"] != "" || rows[0]["cost"] != float64(50) {
		t.Fatalf("legacy summary = %+v", rows)
	}
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?format=csv&application_id=901&environment=prod", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status=%d %s", rr.Code, rr.Body.String())
	}
	csvRows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(csvRows) != 3 {
		t.Fatalf("csv=%+v, err=%v", csvRows, err)
	}
	columns := map[string]int{}
	for i, header := range csvRows[0] {
		columns[header] = i
	}
	for _, key := range []string{"application_id", "environment", "currency"} {
		if _, ok := columns[key]; !ok {
			t.Fatalf("missing CSV column %s", key)
		}
	}
	protected := false
	for _, row := range csvRows[1:] {
		if row[columns["application_id"]] != "901" || row[columns["environment"]] != "prod" {
			t.Fatalf("csv snapshot=%+v", row)
		}
		if row[columns["provider"]] == "'=UPSTREAM()" {
			protected = true
		}
	}
	if !protected {
		t.Fatal("CSV formula was not neutralized")
	}
}

func TestRequestLogs_AttributionCountAndCSV(t *testing.T) {
	h, db, _ := authedAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	tok := login(t, h, "ta@acme", "ta-pass-123")
	if err := db.Exec(`INSERT INTO request_logs (tenant, provider, model_requested, application_id, environment) VALUES
		('acme', '=UPSTREAM()', 'chat', 901, 'prod'),
		('acme', 'openai', 'chat', 901, 'dev'),
		('acme', 'openai', 'chat', NULL, ''),
		('other', 'openai', 'chat', 901, 'prod'),
		('other', 'openai', 'chat', NULL, '')`).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
		app   bool
	}{
		{"application_id=901&environment=prod&tenant=other", 1, true},
		{"unattributed=true&tenant=other", 1, false},
	} {
		rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?"+tc.query, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var envelope struct {
			Data  []store.RequestLogRow `json:"data"`
			Total int64                 `json:"total"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data) != tc.want || envelope.Total != int64(tc.want) {
			t.Fatalf("unexpected count: %+v", envelope)
		}
		row := envelope.Data[0]
		if row.Tenant != "acme" || (row.ApplicationID != nil) != tc.app {
			t.Fatalf("identity leak: %+v", row)
		}
		if tc.app && (*row.ApplicationID != 901 || row.Environment != "prod") {
			t.Fatalf("lost snapshot: %+v", row)
		}
	}
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?format=csv&application_id=901&environment=prod", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status=%d body=%s", rr.Code, rr.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("csv rows=%+v, %v", rows, err)
	}
	columns := map[string]int{}
	for i, header := range rows[0] {
		columns[header] = i
	}
	for _, key := range []string{"application_id", "environment"} {
		if _, ok := columns[key]; !ok {
			t.Fatalf("missing CSV column %s", key)
		}
	}
	if rows[1][columns["application_id"]] != "901" || rows[1][columns["environment"]] != "prod" || rows[1][columns["provider"]] != "'=UPSTREAM()" {
		t.Fatalf("unsafe or incorrect CSV: %+v", rows[1])
	}
}

// seedUsageRow inserts a usage record for a tenant (created_at defaults to now()).
func seedUsageRow(t *testing.T, db *store.DB, tenant, provider string, cost int64) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO usage_records
		   (tenant, group_name, api_key_id, provider, model, prompt_tokens, completion_tokens, cost)
		 VALUES (?, '', 'k', ?, 'gpt-4o', 10, 20, ?)`,
		tenant, provider, cost,
	).Error; err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

// super-admin sees all tenants' usage via the envelope.
func TestUsage_SuperAdminSeesAll(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "other", "openai", 200)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 2 {
		t.Errorf("super-admin usage rows = %d, want 2 (all tenants)", len(rows))
	}
}

// A tenant-admin sees only its own tenant's usage — never another tenant's.
func TestUsage_TenantAdminScoped(t *testing.T) {
	h, db, _ := authedAdmin(t)

	var acmeID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&acmeID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", acmeID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "other", "openai", 999)

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/usage", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 {
		t.Fatalf("tenant-admin usage rows = %d, want 1 (own tenant only)", len(rows))
	}
	if rows[0]["tenant"] != "acme" {
		t.Errorf("tenant-admin saw tenant %v, want acme (cross-tenant leak)", rows[0]["tenant"])
	}
}

// Summary aggregates by the requested dimension.
func TestUsageSummary_GroupByProvider(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "acme", "openai", 200)
	seedUsageRow(t, db, "acme", "claude", 50)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary?group_by=provider", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	byKey := map[string]map[string]any{}
	for _, r := range rows {
		byKey[r["group_key"].(string)] = r
	}
	// JSON numbers decode as float64.
	if got := byKey["openai"]["cost"].(float64); got != 300 {
		t.Errorf("openai cost = %v, want 300", got)
	}
	if got := byKey["claude"]["cost"].(float64); got != 50 {
		t.Errorf("claude cost = %v, want 50", got)
	}
}

// An unknown group_by dimension is rejected 400.
func TestUsageSummary_RejectsUnknownDimension(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary?group_by=bogus", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown group_by status = %d, want 400", rr.Code)
	}
}

// Usage reads are never audited (ADR-0017 §5).
func TestUsage_ReadsNotAudited(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)

	_ = doAuth(t, h, tok, http.MethodGet, "/api/v1/usage", nil)
	_ = doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary", nil)

	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs`).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 0 {
		t.Errorf("audit rows = %d, want 0 (reads must not be audited)", count)
	}
}

// super-admin can scope usage to a single tenant via ?tenant=NAME.
func TestUsage_SuperAdminFilterByTenant(t *testing.T) {
	h, db, tok := authedAdmin(t)
	if err := db.Exec(`INSERT INTO tenants (name) VALUES ('acme')`).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "other", "openai", 200)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?tenant=acme", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 {
		t.Errorf("filtered rows = %d, want 1", len(rows))
	}
	if len(rows) > 0 && rows[0]["tenant"] != "acme" {
		t.Errorf("saw tenant %v, want acme", rows[0]["tenant"])
	}
}

// An unknown tenant filter returns 400.
func TestUsage_UnknownTenantFilter(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?tenant=nonexistent", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown tenant status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// provider query param filters usage rows.
func TestUsage_ProviderFilter(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "acme", "claude", 200)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?provider=claude", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 {
		t.Errorf("filtered rows = %d, want 1", len(rows))
	}
	if len(rows) > 0 && rows[0]["provider"] != "claude" {
		t.Errorf("saw provider %v, want claude", rows[0]["provider"])
	}
}
