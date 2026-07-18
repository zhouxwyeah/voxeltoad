package apperr

// Tenant / group / tenancy-scoped errors.
var (
	TenantNotFound             = New("tenant_not_found", StatusNotFound, "errors.tenant.tenantNotFound")
	GroupNotFound              = New("group_not_found", StatusNotFound, "errors.tenant.groupNotFound")
	GroupReferenced            = New("group_referenced", StatusConflict, "errors.tenant.groupReferenced")
	ScopeOutsideTenant         = New("scope_outside_tenant", StatusForbidden, "errors.tenant.scopeOutsideTenant")
	TenantInvalidForSuperAdmin = New("tenant_invalid_for_super_admin", StatusBadRequest, "errors.tenant.tenantInvalidForSuperAdmin")
	TenantRequired             = New("tenant_required", StatusBadRequest, "errors.tenant.tenantRequired")
	LastSuperAdmin             = New("last_super_admin", StatusConflict, "errors.tenant.lastSuperAdmin")
	APIKeyNotFoundInTenant     = New("api_key_not_found_in_tenant", StatusNotFound, "errors.tenant.apiKeyNotFoundInTenant")
	TenantAdminHasNoTenant     = New("tenant_admin_has_no_tenant", StatusForbidden, "errors.tenant.tenantAdminHasNoTenant")
	NotAuthorizedToReadUsage   = New("not_authorized_to_read_usage", StatusForbidden, "errors.tenant.notAuthorizedToReadUsage")
)
