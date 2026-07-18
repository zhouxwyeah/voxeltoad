package apperr

// Operator-domain errors (management-plane operator CRUD, distinct from
// client API-key auth in internal/auth).
var (
	OperatorNotFound = New("operator_not_found", StatusNotFound, "errors.operator.operatorNotFound")
)
