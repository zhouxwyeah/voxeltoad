package apperr

var (
	BudgetInvalidBody      = New("budget_invalid_body", StatusBadRequest, "errors.budget.invalidBody")
	BudgetInvalidPolicy    = New("budget_invalid_policy", StatusBadRequest, "errors.budget.invalidPolicy")
	BudgetInvalidQuery     = New("budget_invalid_query", StatusBadRequest, "errors.budget.invalidQuery")
	BudgetTenantRequired   = New("budget_tenant_required", StatusBadRequest, "errors.budget.tenantRequired")
	BudgetTenantMismatch   = New("budget_tenant_mismatch", StatusForbidden, "errors.budget.tenantMismatch")
	BudgetNotFound         = New("budget_not_found", StatusNotFound, "errors.budget.notFound")
	BudgetConflict         = New("budget_conflict", StatusConflict, "errors.budget.conflict")
	BudgetCurrencyMismatch = New("budget_currency_mismatch", StatusConflict, "errors.budget.currencyMismatch")
	BudgetInsufficient     = New("budget_insufficient", StatusConflict, "errors.budget.insufficient")
)
