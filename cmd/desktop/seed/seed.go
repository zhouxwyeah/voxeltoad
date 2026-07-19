// Package seed initializes the desktop gateway's local state on first run:
// a single default API key (K1, design/desktop.md §8) and a default dynamic
// config template (if the user has not supplied one).
package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"voxeltoad/internal/desktopstore"
)

// DefaultKey returns the plaintext default key: from GATEWAY_DESKTOP_KEY if set,
// otherwise a fixed dev constant. The plaintext is logged once at startup so
// the user can configure their agents' Authorization header.
func DefaultKey() string {
	if k := os.Getenv("GATEWAY_DESKTOP_KEY"); k != "" {
		return k
	}
	return "desktop-local-default-key"
}

// Key hashes the plaintext key and upserts the single default API key row.
// Empty AllowedModels ("[]") grants unrestricted model access.
func Key(ctx context.Context, db *desktopstore.DB, plaintext string) error {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	row := desktopstore.APIKeyRow{
		KeyID:         "default",
		Hash:          hash,
		Tenant:        "default",
		Group:         "default",
		ExpiresAt:     nil,
		AllowedModels: "[]",
	}
	// Idempotent upsert: keep the same default key across restarts. The row
	// uses an auto-increment PK, so Save would always INSERT and collide on the
	// unique key_id after the first run; FirstOrCreate+Assign upserts instead.
	return db.WithContext(ctx).
		Where(desktopstore.APIKeyRow{KeyID: "default"}).
		Assign(row).
		FirstOrCreate(&row).Error
}

// ConfigTemplate is the default dynamic config written on first run. It seeds
// the same realistic test dataset used by cmd/adminstack/seed.go: four real
// providers (深度求索 / TokenHub / Kimi-code / GLM) with five model aliases
// spanning all routing strategies (priority / round_robin / session_affinity),
// so the desktop UI is useful out of the box rather than showing a single empty
// route. hy3 is seeded as a catalog entry only (no route) to mirror admin.
// Upstream keys are referenced via ${GATEWAY_SEED_*_KEY} env vars and expanded
// by EnsureTemplate before the file is written; unset vars yield an empty
// plain:// ref, so the gateway fails fast with a clear auth error rather than
// silently using a missing key.
const ConfigTemplate = `# Desktop personal gateway — configuration.
# Seeded with real test providers (mirrors cmd/adminstack/seed.go).
# Providers reference real keys via GATEWAY_SEED_*_KEY env vars.
# Copy .env.example → .env, fill in real keys, and restart.
# Edit and restart to change routing / capture. Read once at startup.
gateway:
  addr: "127.0.0.1:8787"
  session_headers:
    - X-Voxeltoad-Session
providers:
  - name: 深度求索
    type: deepseek
    adapter: openai
    base_url: https://api.deepseek.com
    api_key_ref: "plain://${GATEWAY_SEED_DEEPSEEK_KEY}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
  - name: TokenHub
    type: tencent
    adapter: openai
    base_url: https://tokenhub.tencentmaas.com/v1
    api_key_ref: "plain://${GATEWAY_SEED_TOKENHUB_KEY}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
  - name: Kimi-code
    type: Kimi
    adapter: openai
    base_url: https://api.kimi.com/coding/v1
    api_key_ref: "plain://${GATEWAY_SEED_KIMI_KEY}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
  - name: GLM
    type: zhipu
    adapter: openai
    base_url: https://open.bigmodel.cn/api/coding/paas/v4
    api_key_ref: "plain://${GATEWAY_SEED_GLM_KEY}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
models:
  - alias: deepseek-v4-flash
    upstreams:
      - provider: 深度求索
        upstream_model: deepseek-v4-flash
        pricing: {prompt_per_1m: 2500000, completion_per_1m: 10000000, currency: usd}
  - alias: deepseek-v4-pro
    upstreams:
      - provider: 深度求索
        upstream_model: deepseek-v4-pro
        pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}
  - alias: hy3
    upstreams:
      - provider: TokenHub
        upstream_model: hy3
        pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}
  - alias: kimi-for-coding
    upstreams:
      - provider: Kimi-code
        upstream_model: kimi-for-coding
        pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}
  - alias: glm-5.2
    upstreams:
      - provider: GLM
        upstream_model: glm5.2
        pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}
routes:
  - model_alias: deepseek-v4-flash
    strategy: priority
    providers:
      - {name: 深度求索, weight: 1}
  - model_alias: deepseek-v4-pro
    strategy: round_robin
    providers:
      - {name: 深度求索, weight: 1}
  - model_alias: kimi-for-coding
    strategy: session_affinity
    providers:
      - {name: Kimi-code, weight: 1}
  - model_alias: glm-5.2
    strategy: session_affinity
    providers:
      - {name: GLM, weight: 1}
settings:
  trace:
    capture_payload_enabled: true
    max_body_kb: 256
    retention_days: 30
`

// EnsureTemplate writes the default config template to path if it does not
// already exist, so first run produces a working (if minimal) configuration.
// ${VAR} placeholders in the template are expanded from the environment
// before writing; unset vars become empty strings.
func EnsureTemplate(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil // user-provided config already present
	}
	content := os.ExpandEnv(ConfigTemplate)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("seed: write template %s: %w", path, err)
	}
	return nil
}
