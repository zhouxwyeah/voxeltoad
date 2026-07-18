//go:build dbtest

package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// specPath locates docs/openapi/admin.yaml relative to this test file, so the
// contract test finds the spec regardless of the working directory.
func specPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	// internal/admin/<this> → repo root is three levels up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "docs", "openapi", "admin.yaml")
}

func loadSpec(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("spec is not a valid OpenAPI 3 document: %v", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router from spec: %v", err)
	}
	return doc, router
}

// The spec itself must be a valid OpenAPI 3 document — this is the authority
// baseline (ADR-0019, OpenAPI-first). A broken spec fails CI here.
func TestContract_SpecIsValid(t *testing.T) {
	_, _ = loadSpec(t)
}

// validateResponse checks a recorded admin response against the spec: the route
// must be described, and the response body/status must satisfy the schema.
func validateResponse(t *testing.T, router routers.Router, req *http.Request, rr *httptest.ResponseRecorder) {
	t.Helper()
	route, pathParams, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("route %s %s not described in spec: %v", req.Method, req.URL.Path, err)
	}
	reqInput := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: pathParams,
		Route:      route,
	}
	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 rr.Code,
		Header:                 rr.Header(),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	respInput.SetBodyBytes(rr.Body.Bytes())
	if err := openapi3filter.ValidateResponse(context.Background(), respInput); err != nil {
		t.Errorf("%s %s → %d: response does not match spec: %v\nbody=%s",
			req.Method, req.URL.Path, rr.Code, err, rr.Body.String())
	}
}

// Real admin responses must conform to the published spec. This is what makes
// the spec authoritative rather than aspirational: if a handler drifts from the
// contract the generated SDK depends on, this fails.
func TestContract_ResponsesMatchSpec(t *testing.T) {
	_, router := loadSpec(t)
	h, db, tok := authedAdmin(t)

	// Seed enough state that list/read endpoints return representative bodies.
	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "openai-prod", "type": "openai", "adapter": "openai",
		"base_url": "https://api.openai.com/v1", "api_key_ref": "env://K",
	}); rr.Code != http.StatusCreated {
		t.Fatalf("seed provider: %d %s", rr.Code, rr.Body.String())
	}
	// Seed tenant so tenant:acme scope validation passes for the topup below.
	if err := db.Exec(`INSERT INTO tenants (name) VALUES ('acme')`).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 1000, "currency": "usd",
	}); rr.Code != http.StatusOK {
		t.Fatalf("seed topup: %d %s", rr.Code, rr.Body.String())
	}

	// Exercise a representative set of GET endpoints and validate each response.
	gets := []string{
		"/api/v1/providers",
		"/api/v1/models",
		"/api/v1/routes",
		"/api/v1/plugins",
		"/api/v1/tenants",
		"/api/v1/operators",
		"/api/v1/usage",
		"/api/v1/usage/summary?group_by=provider",
		"/api/v1/audit",
		"/api/v1/request-logs",
		"/api/v1/quotas?scope=tenant:acme",
	}
	for _, path := range gets {
		rr := doAuth(t, h, tok, http.MethodGet, path, nil)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200; body=%s", path, rr.Code, rr.Body.String())
			continue
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		validateResponse(t, router, req, rr)
	}
}
