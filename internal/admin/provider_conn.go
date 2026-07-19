package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/apperr"
	"voxeltoad/internal/config"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/store"
)

// providerConnResult is the outcome of a provider connectivity probe. A failed
// probe is a business result (ok=false), not an API error, so the endpoints
// always answer 200 with this payload — only malformed requests / unknown
// providers produce typed errors.
type providerConnResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

// providerConnRequest is the payload for the ad-hoc connectivity test endpoint
// (unsaved form values from the create/edit modal). APIKey is used in-memory
// for the single probe request only — never persisted, logged, or returned.
type providerConnRequest struct {
	Name      string `json:"name,omitempty"`
	Adapter   string `json:"adapter"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	APIKeyRef string `json:"api_key_ref,omitempty"`
}

// probeTimeout bounds the whole probe exchange (connect + response).
const probeTimeout = 10 * time.Second

// probeHTTPClient is shared across probes; its Timeout is a safety net on top
// of the per-request context deadline.
var probeHTTPClient = &http.Client{Timeout: probeTimeout}

// mountProviderConnTest wires the provider connectivity-test endpoints on the
// same (super-admin) group as provider CRUD:
//
//	POST /providers/:name/test — probe a saved provider (credential resolved
//	                             server-side, so masked list rows can be tested)
//	POST /provider-tests       — probe an unsaved (ad-hoc) provider config
//
// The ad-hoc path lives outside /providers/ because gin's radix tree rejects a
// static "test" segment next to the ":name" wildcard. Probes are diagnostics,
// not config mutations, so they are not audit-logged.
func mountProviderConnTest(g *gin.RouterGroup, repo *store.ConfigRepo, credService credential.Service, credRepo *store.CredentialRepo) {
	g.POST("/providers/:name/test", func(c *gin.Context) {
		name := c.Param("name")
		p, ok, err := repo.GetProvider(c.Request.Context(), name)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !ok {
			appErr(c, apperr.ProviderNotFound)
			return
		}
		key, err := resolveProviderKey(c.Request.Context(), p.Name, p.APIKeyRef, credService, credRepo)
		if err != nil {
			c.JSON(http.StatusOK, providerConnResult{OK: false, Error: "resolve credential: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, probeProvider(c.Request.Context(), p.Adapter, p.BaseURL, key))
	})

	g.POST("/provider-tests", func(c *gin.Context) {
		var req providerConnRequest
		if !bind(c, &req) {
			return
		}
		if !isKnownAdapter(req.Adapter) {
			badRequest(c, "unknown adapter: "+req.Adapter)
			return
		}
		if req.BaseURL == "" {
			badRequest(c, "base_url is required")
			return
		}
		key, err := resolveConnRequestKey(c.Request.Context(), req, repo, credService, credRepo)
		if err != nil {
			c.JSON(http.StatusOK, providerConnResult{OK: false, Error: "resolve credential: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, probeProvider(c.Request.Context(), req.Adapter, req.BaseURL, key))
	})
}

// probeProvider issues GET {baseURL}/models with adapter-appropriate auth and
// classifies the outcome. /models is used because both the OpenAI and
// Anthropic APIs (and most OpenAI-compatible vendors) implement it, it
// requires no model name, and it costs no inference tokens — while still
// validating reachability (DNS/TCP/TLS) and credential validity (401/403).
//
// apiKey is sensitive: it is only placed on the outbound request, never logged
// or reflected into the result.
func probeProvider(ctx context.Context, adapterName, baseURL, apiKey string) providerConnResult {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/models")
	if err != nil || u.Scheme == "" || u.Host == "" {
		return providerConnResult{OK: false, Error: "invalid base_url"}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return providerConnResult{OK: false, Error: "invalid base_url"}
	}
	if apiKey != "" {
		if adapterName == "claude" {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			// openai and OpenAI-compatible adapters.
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	start := time.Now()
	resp, err := probeHTTPClient.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return providerConnResult{OK: false, LatencyMs: latency, Error: "unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	res := providerConnResult{LatencyMs: latency, Status: resp.StatusCode}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		res.OK = true
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.Error = fmt.Sprintf("authentication failed (upstream returned %d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		res.Error = fmt.Sprintf("upstream server error (%d)", resp.StatusCode)
	default:
		// 3xx/404/429 etc.: the upstream answered and did not reject the
		// credential, which is what a connectivity test asserts (some vendors
		// do not implement /models and answer 404 while fully reachable).
		res.OK = true
	}
	if !res.OK {
		res.Error += bodySnippet(resp)
	}
	return res
}

// bodySnippet reads a bounded prefix of an error response body for context.
func bodySnippet(resp *http.Response) string {
	b, err := io.ReadAll(io.LimitReader(resp.Body, 200))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	return ": " + s
}

// resolveProviderKey turns a stored api_key_ref into a plaintext key. db://
// refs are decrypted from the credential table; everything else goes through
// the scheme registry (env://, plain://, bare literal). An empty ref yields an
// empty key (unauthenticated probe, e.g. key-less local upstreams).
func resolveProviderKey(ctx context.Context, name, ref string, credService credential.Service, credRepo *store.CredentialRepo) (string, error) {
	if ref == "" {
		return "", nil
	}
	if _, isDB := config.ParseDBProviderRef(ref); isDB {
		if credService == nil || credRepo == nil {
			return "", fmt.Errorf("credential store not configured")
		}
		cred, ok, err := credRepo.Get(ctx, name)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("stored credential for provider %q not found", name)
		}
		return credService.Decrypt(cred)
	}
	return config.ResolveSecret(ref)
}

// resolveConnRequestKey picks the credential material for an ad-hoc probe, in
// order: a plaintext api_key (used directly, never persisted); then a
// non-masked api_key_ref via the scheme registry; then, when a provider name
// is given (edit modal leaving the key untouched), the stored credential.
func resolveConnRequestKey(ctx context.Context, req providerConnRequest, repo *store.ConfigRepo, credService credential.Service, credRepo *store.CredentialRepo) (string, error) {
	if req.APIKey != "" {
		return req.APIKey, nil
	}
	ref := req.APIKeyRef
	if isMaskedRef(ref) {
		// Masked placeholders shown by the edit form ("env://***", "***") are
		// not resolvable refs — drop them and fall back to the stored one.
		ref = ""
	}
	if ref != "" {
		key, err := config.ResolveSecret(ref)
		if err == nil {
			return key, nil
		}
		if req.Name == "" {
			return "", err
		}
		// Otherwise fall through to the stored credential below (e.g. the ref
		// points at an env var that only exists on the data-plane machine).
	}
	if req.Name != "" {
		p, ok, err := repo.GetProvider(ctx, req.Name)
		if err != nil {
			return "", err
		}
		if ok {
			return resolveProviderKey(ctx, p.Name, p.APIKeyRef, credService, credRepo)
		}
	}
	return "", nil
}

// isMaskedRef reports whether ref is an ADR-0031 masked placeholder rather
// than a resolvable reference: "***", "env://***", "plain://***", etc.
func isMaskedRef(ref string) bool {
	return ref == "***" || strings.HasSuffix(ref, "://***")
}
