package authz

import "testing"

func TestAllPermissions_Unique(t *testing.T) {
	seen := map[Permission]bool{}
	for _, e := range AllPermissions() {
		if seen[e.Perm] {
			t.Errorf("duplicate permission %q in catalog", e.Perm)
		}
		seen[e.Perm] = true
	}
}

func TestAllPermissions_Format(t *testing.T) {
	for _, e := range AllPermissions() {
		p := string(e.Perm)
		// Must be resource.action with exactly one dot.
		dots := 0
		for _, c := range p {
			if c == '.' {
				dots++
			}
		}
		if dots != 1 {
			t.Errorf("permission %q must have exactly one dot (resource.action)", p)
		}
	}
}

func TestAllPermissions_ScopeDefined(t *testing.T) {
	for _, e := range AllPermissions() {
		switch e.Scope {
		case ScopeGlobal, ScopeTenant:
			// ok
		default:
			t.Errorf("permission %q has unknown scope %q", e.Perm, e.Scope)
		}
	}
}

func TestWildcard_HasAll(t *testing.T) {
	for _, e := range AllPermissions() {
		if !Wildcard.Has(e.Perm) {
			t.Errorf("wildcard must match %q", e.Perm)
		}
	}
}

func TestPermission_HasSelf(t *testing.T) {
	for _, e := range AllPermissions() {
		if !e.Perm.Has(e.Perm) {
			t.Errorf("%q must match itself", e.Perm)
		}
	}
}

func TestPermission_HasNonMatching(t *testing.T) {
	if PermProviderRead.Has(PermModelWrite) {
		t.Error("provider.read must not match model.write")
	}
}
