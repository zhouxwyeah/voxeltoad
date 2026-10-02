package apperr

// Application governance errors (ADR-0051).
var (
	ApplicationNotFound      = New("application_not_found", StatusNotFound, "errors.application.applicationNotFound")
	ApplicationReferenced    = New("application_referenced", StatusConflict, "errors.application.applicationReferenced")
	ApplicationNameRequired  = New("application_name_required", StatusBadRequest, "errors.application.applicationNameRequired")
	ApplicationOwnerRequired = New("application_owner_required", StatusBadRequest, "errors.application.applicationOwnerRequired")
)
