// Package authz defines the management-plane permission catalog — the single
// source of truth for what actions an operator may perform. Permissions are
// `resource.action` strings defined as constants; roles carry a set of them.
package authz

// Permission is a resource.action key from the catalog.
type Permission string

// ScopeKind declares whether a role (and therefore its holder) acts at global
// or tenant scope — the structural isolation axis that scoped-repository
// guarantees depend on.
type ScopeKind string

const (
	ScopeGlobal ScopeKind = "global"
	ScopeTenant ScopeKind = "tenant"
)

// --- Permission catalog (resource.action, paired with its scope) ---

const (
	// Global-scope: manage platform config (provider/model/route/plugin CRUD).
	PermProviderRead  Permission = "provider.read"
	PermProviderWrite Permission = "provider.write"
	PermModelRead     Permission = "model.read"
	PermModelWrite    Permission = "model.write"
	PermRouteRead     Permission = "route.read"
	PermRouteWrite    Permission = "route.write"
	PermPluginRead    Permission = "plugin.read"
	PermPluginWrite   Permission = "plugin.write"

	// Global-scope: administer tenants, operators, and quotas globally.
	PermTenantRead    Permission = "tenant.read"
	PermTenantWrite   Permission = "tenant.write"
	PermOperatorRead  Permission = "operator.read"
	PermOperatorWrite Permission = "operator.write"
	PermQuotaWrite    Permission = "quota.write"

	// Global-scope: read-only operational views.
	PermConfigHistoryRead Permission = "config_history.read"
	PermDataplaneRead     Permission = "dataplane.read"
	PermOverviewRead      Permission = "overview.read"
	PermSettingsRead      Permission = "settings.read"

	// Tenant-scope: manage api-keys and groups within a tenant.
	PermAPIKeyRead  Permission = "api_key.read"
	PermAPIKeyWrite Permission = "api_key.write"
	PermGroupRead   Permission = "group.read"
	PermGroupWrite  Permission = "group.write"

	// Both-scope: read-only cross-cutting views (scope governs the view width).
	PermUsageRead      Permission = "usage.read"
	PermAuditRead      Permission = "audit.read"
	PermRequestLogRead Permission = "request_log.read"
	PermQuotaRead      Permission = "quota.read"

	// Both-scope: operator self-service.
	PermPasswordWrite Permission = "password.write"

	// Both-scope: role management (super-admin creates/manages roles).
	PermRoleRead  Permission = "role.read"
	PermRoleWrite Permission = "role.write"
)

// Wildcard is the sentinel that means "all permissions". The built-in
// super-admin role carries only this one entry; checkers that encounter it
// always return true. This avoids maintaining an ever-growing explicit list.
const Wildcard Permission = "*"

// Entry associates a Permission with its scope and a human label.
type Entry struct {
	Perm  Permission
	Scope ScopeKind
	Label string
}

// AllPermissions returns the ordered catalog. New permissions MUST be added
// here AND to the check-permissions gate, otherwise the gate will fail.
func AllPermissions() []Entry {
	return []Entry{
		// Global-scope
		{PermProviderRead, ScopeGlobal, "Read providers"},
		{PermProviderWrite, ScopeGlobal, "Write providers"},
		{PermModelRead, ScopeGlobal, "Read models"},
		{PermModelWrite, ScopeGlobal, "Write models"},
		{PermRouteRead, ScopeGlobal, "Read routes"},
		{PermRouteWrite, ScopeGlobal, "Write routes"},
		{PermPluginRead, ScopeGlobal, "Read plugins"},
		{PermPluginWrite, ScopeGlobal, "Write plugins"},
		{PermTenantRead, ScopeGlobal, "Read tenants"},
		{PermTenantWrite, ScopeGlobal, "Write tenants"},
		{PermOperatorRead, ScopeGlobal, "Read operators"},
		{PermOperatorWrite, ScopeGlobal, "Write operators"},
		{PermQuotaWrite, ScopeGlobal, "Write quotas (global top-up)"},
		{PermConfigHistoryRead, ScopeGlobal, "Read config history"},
		{PermDataplaneRead, ScopeGlobal, "Read data-plane nodes"},
		{PermOverviewRead, ScopeGlobal, "Read overview dashboard"},
		{PermSettingsRead, ScopeGlobal, "Read gateway settings"},
		// Tenant-scope
		{PermAPIKeyRead, ScopeTenant, "Read API keys"},
		{PermAPIKeyWrite, ScopeTenant, "Write API keys"},
		{PermGroupRead, ScopeTenant, "Read groups"},
		{PermGroupWrite, ScopeTenant, "Write groups"},
		// Both-scope
		{PermUsageRead, ScopeGlobal, "Read usage stats"}, // global → all; tenant → own
		{PermAuditRead, ScopeGlobal, "Read audit trail"},
		{PermRequestLogRead, ScopeGlobal, "Read request logs"},
		{PermQuotaRead, ScopeGlobal, "Read quotas"},
		{PermPasswordWrite, ScopeGlobal, "Change own password"},
		{PermRoleRead, ScopeGlobal, "Read roles"},
		{PermRoleWrite, ScopeGlobal, "Write roles"},
	}
}

// PermissionsForScope returns the subset of AllPermissions() with the given
// scope. Both-scope entries appear in both views.
func PermissionsForScope(s ScopeKind) []Permission {
	var out []Permission
	for _, e := range AllPermissions() {
		if e.Scope == s || e.Scope == ScopeGlobal { // both-scope maps to both
			out = append(out, e.Perm)
		}
	}
	return out
}

// Has reports whether the set contains perm (or the wildcard).
func (p Permission) Has(perm Permission) bool {
	return p == Wildcard || p == perm
}
