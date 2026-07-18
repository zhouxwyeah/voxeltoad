package apperr

// Auth-domain errors (admin login, session, RBAC).
var (
	EmailPasswordRequired = New("email_password_required", StatusBadRequest, "errors.auth.emailPasswordRequired")
	InvalidCredentials    = New("invalid_credentials", StatusUnauthorized, "errors.auth.invalidCredentials")
	TooManyLogins         = New("too_many_logins", StatusTooManyRequests, "errors.auth.tooManyLogins")
	MissingBearerToken    = New("missing_bearer_token", StatusUnauthorized, "errors.auth.missingBearerToken")
	InvalidSession        = New("invalid_session", StatusUnauthorized, "errors.auth.invalidSession")
	SuperAdminRequired    = New("super_admin_required", StatusForbidden, "errors.auth.superAdminRequired")
	TenantAdminRequired   = New("tenant_admin_required", StatusForbidden, "errors.auth.tenantAdminRequired")
	PermissionDenied      = New("permission_denied", StatusForbidden, "errors.auth.permissionDenied")
	UnknownOperatorRole   = New("unknown_operator_role", StatusForbidden, "errors.auth.unknownOperatorRole")
)
