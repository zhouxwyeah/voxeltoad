package apperr

// Route-domain errors.
var (
	RouteAliasRequired = New("route_alias_required", StatusBadRequest, "errors.route.routeAliasRequired")
	InvalidStrategy    = New("invalid_strategy", StatusBadRequest, "errors.route.invalidStrategy")
	InvalidPhase       = New("invalid_phase", StatusBadRequest, "errors.route.invalidPhase")
	RouteCreateFailed  = New("route_create_failed", StatusBadRequest, "errors.route.createFailed")
	RouteDeleteFailed  = New("route_delete_failed", StatusBadRequest, "errors.route.deleteFailed")
	RouteUpdateFailed  = New("route_update_failed", StatusBadRequest, "errors.route.updateFailed")
	RouteNotFound      = New("route_not_found", StatusNotFound, "errors.route.notFound")
)
