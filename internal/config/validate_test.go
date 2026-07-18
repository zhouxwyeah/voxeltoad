package config

import (
	"os"
	"testing"
)

// TestValidate_RejectsEmptyInternalToken asserts the snapshot channel's shared
// secret is REQUIRED in production (ADR-0007): an empty internal_token_ref with
// AllowInsecureDev=false must fail Validate. This is the production-safety gate.
func TestValidate_RejectsEmptyInternalToken(t *testing.T) {
	b := Default()
	b.Gateway.AllowInsecureDev = false
	if err := b.Validate(); err == nil {
		t.Error("expected Validate to reject empty internal_token_ref in production")
	}
}

// TestValidate_AllowsEmptyTokenWhenDevEnabled asserts the dev escape hatch
// (GATEWAY_ALLOW_INSECURE_DEV=1 / AllowInsecureDev=true) bypasses the gate, so
// devstack can run with an open channel.
func TestValidate_AllowsEmptyTokenWhenDevEnabled(t *testing.T) {
	b := Default()
	b.Gateway.AllowInsecureDev = true
	if err := b.Validate(); err != nil {
		t.Errorf("expected Validate to pass in dev mode, got: %v", err)
	}
}

// TestValidate_PassesWithToken asserts a configured token passes.
func TestValidate_PassesWithToken(t *testing.T) {
	b := Default()
	b.Snapshot.InternalTokenRef = "env://GATEWAY_INTERNAL_TOKEN"
	if err := b.Validate(); err != nil {
		t.Errorf("expected Validate to pass with a token, got: %v", err)
	}
}

// TestLoad_HonorsAllowInsecureDevEnv asserts the GATEWAY_ALLOW_INSECURE_DEV=1 env
// var flips the gate at config load time (so the gateway binary can start in
// dev mode without YAML changes).
func TestLoad_HonorsAllowInsecureDevEnv(t *testing.T) {
	// Write a minimal config file with NO internal_token_ref.
	f, err := os.CreateTemp("", "voxeltoad-cfg-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString("snapshot:\n  admin_url: http://localhost:8090\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Without the env var: Load must fail.
	os.Setenv("GATEWAY_ALLOW_INSECURE_DEV", "")
	defer os.Unsetenv("GATEWAY_ALLOW_INSECURE_DEV")
	if _, err := Load(f.Name()); err == nil {
		t.Error("expected Load to fail without GATEWAY_ALLOW_INSECURE_DEV when token is empty")
	}

	// With the env var: Load must pass.
	os.Setenv("GATEWAY_ALLOW_INSECURE_DEV", "1")
	if _, err := Load(f.Name()); err != nil {
		t.Errorf("expected Load to pass with GATEWAY_ALLOW_INSECURE_DEV=1, got: %v", err)
	}
}
