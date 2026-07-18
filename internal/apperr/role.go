package apperr

// Role-domain errors (custom roles management).
var (
	RoleNotFound         = New("role_not_found", StatusNotFound, "errors.roles.roleNotFound")
	RoleInUse            = New("role_in_use", StatusConflict, "errors.roles.roleInUse")
	BuiltinRoleImmutable = New("builtin_role_immutable", StatusForbidden, "errors.roles.builtinRoleImmutable")
	InvalidPermission    = New("invalid_permission", StatusBadRequest, "errors.roles.invalidPermission")
)
