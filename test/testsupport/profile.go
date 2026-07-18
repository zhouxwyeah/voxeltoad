// Package testsupport provides shared E2E test scaffolding: profile loading,
// feature-flag derivation, and the mock upstream server. See design/e2e.md.
//
// Profiles live in test/profiles/*.yaml. The default profile mocks every
// upstream with fake keys so CI can run the full suite without real provider
// credentials. Feature flags are derived from the config: a placeholder key
// marks the provider "not real", and tests requiring it are skipped.
package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile is the parsed E2E profile.
type Profile struct {
	Gateway struct {
		BaseURL     string `yaml:"base_url"`
		AdminAPIKey string `yaml:"admin_api_key"`
	} `yaml:"gateway"`
	TestRailRunID string                    `yaml:"testrail_run_id"`
	Providers     map[string]ProviderConfig `yaml:"providers"`

	// Features is derived after load (not from YAML).
	Features Features `yaml:"-"`
}

// ProviderConfig holds one upstream provider's test config.
type ProviderConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

// Features reports which providers have real (non-placeholder) credentials.
type Features struct {
	HasRealOpenAI  bool
	HasRealClaude  bool
	HasRealTencent bool
	HasRealZhipu   bool
}

// LoadProfile reads the profile selected by the E2E_PROFILE_PATH env var, or
// the default profile when unset. repoRoot locates the default profile.
func LoadProfile(repoRoot string) (*Profile, error) {
	path := os.Getenv("E2E_PROFILE_PATH")
	if path == "" {
		path = filepath.Join(repoRoot, "test", "profiles", "default.yaml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("testsupport: read profile %s: %w", path, err)
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("testsupport: parse profile %s: %w", path, err)
	}
	p.Features = deriveFeatures(p.Providers)
	return &p, nil
}

// deriveFeatures marks a provider "real" when its key is present and does not
// look like a placeholder (fake*, sk-fake*, REPLACE_ME, empty).
func deriveFeatures(providers map[string]ProviderConfig) Features {
	real := func(name string) bool {
		p, ok := providers[name]
		if !ok {
			return false
		}
		return isRealKey(p.APIKey)
	}
	return Features{
		HasRealOpenAI:  real("openai"),
		HasRealClaude:  real("claude"),
		HasRealTencent: real("tencent"),
		HasRealZhipu:   real("zhipu"),
	}
}

func isRealKey(k string) bool {
	if k == "" {
		return false
	}
	lower := strings.ToLower(k)
	for _, placeholder := range []string{"fake", "sk-fake", "replace_me"} {
		if strings.Contains(lower, placeholder) {
			return false
		}
	}
	return true
}
