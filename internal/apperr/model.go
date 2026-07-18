package apperr

// Model-domain errors.
var (
	ModelAliasRequired   = New("model_alias_required", StatusBadRequest, "errors.model.aliasRequired")
	InvalidPricingAmount = New("invalid_pricing_amount", StatusBadRequest, "errors.model.invalidPricingAmount")
	ModelCreateFailed    = New("model_create_failed", StatusBadRequest, "errors.model.createFailed")
	ModelDeleteFailed    = New("model_delete_failed", StatusConflict, "errors.model.deleteFailed")
	ModelUpdateFailed    = New("model_update_failed", StatusBadRequest, "errors.model.updateFailed")
	ModelNotFound        = New("model_not_found", StatusNotFound, "errors.model.notFound")
)
