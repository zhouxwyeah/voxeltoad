package apperr

// Provider-domain errors (CRUD + validation).
var (
	ProviderNameRequired = New("provider_name_required", StatusBadRequest, "errors.provider.nameRequired")
	ProviderCreateFailed = New("provider_create_failed", StatusBadRequest, "errors.provider.createFailed")
	ProviderNotFound     = New("provider_not_found", StatusNotFound, "errors.provider.notFound")
	ProviderDeleteFailed = New("provider_delete_failed", StatusConflict, "errors.provider.deleteFailed")
	ProviderUpdateFailed = New("provider_update_failed", StatusBadRequest, "errors.provider.updateFailed")
)
