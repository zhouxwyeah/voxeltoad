package store_test

import (
	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

// QuotaRepo must satisfy billing.QuotaStore — the data plane injects it as the
// production quota backend (ADR-0013/0016). Compile-time assertion (no DB needed).
var _ billing.QuotaStore = (*store.QuotaRepo)(nil)
