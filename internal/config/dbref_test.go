package config

import "testing"

func TestDBProviderRef(t *testing.T) {
	if got := DBProviderRef("openai-prod"); got != "db://provider/openai-prod" {
		t.Errorf("DBProviderRef = %q", got)
	}
}

func TestParseDBProviderRef(t *testing.T) {
	name, ok := ParseDBProviderRef("db://provider/openai-prod")
	if !ok || name != "openai-prod" {
		t.Errorf("ParseDBProviderRef = (%q, %v), want (openai-prod, true)", name, ok)
	}

	if _, ok := ParseDBProviderRef("env://OPENAI_KEY"); ok {
		t.Error("env:// should not parse as db provider ref")
	}

	// ParseDBProviderRef intentionally leaves empty-name validation to callers
	// so that masking helpers can preserve the structure.
	if _, ok := ParseDBProviderRef("db://provider/"); !ok {
		t.Error("empty provider name is still a valid parse")
	}
}

func TestParseDBProviderPath(t *testing.T) {
	name, ok := ParseDBProviderPath("provider/openai-prod")
	if !ok || name != "openai-prod" {
		t.Errorf("ParseDBProviderPath = (%q, %v), want (openai-prod, true)", name, ok)
	}

	if _, ok := ParseDBProviderPath("openai-prod"); ok {
		t.Error("path without provider/ prefix should be rejected")
	}

	if _, ok := ParseDBProviderPath("provider/"); ok {
		t.Error("empty provider name should be rejected")
	}
}
