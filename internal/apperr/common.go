package apperr

// Common / cross-domain errors. Avoid adding domain-specific codes here.
var (
	NameRequired = New("name_required", StatusBadRequest, "errors.common.nameRequired")
	Unexpected   = New("unexpected", StatusInternalServerError, "errors.common.unexpected")
	InvalidBody  = New("invalid_body", StatusBadRequest, "errors.common.invalidBody")
)
