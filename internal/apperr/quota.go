package apperr

// Quota-domain errors.
var (
	QuotaInsufficient = New("quota_insufficient", StatusPaymentRequired, "errors.quota.insufficient")
)
