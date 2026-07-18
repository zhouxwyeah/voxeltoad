package store

import (
	"context"
	"encoding/json"
)

// AuditRepo appends management-plane audit entries (ADR-0017 §5): every config/
// identity mutation writes one append-only row. Reads are not audited.
type AuditRepo struct {
	db *DB
}

// NewAuditRepo builds an AuditRepo over the given connection.
func NewAuditRepo(db *DB) *AuditRepo { return &AuditRepo{db: db} }

// AuditEntry is one audit record. Before is optional (phase-1 records After —
// the request payload; a before-snapshot is a later enhancement).
type AuditEntry struct {
	OperatorID   *int64
	Action       string // create | update | delete | read (read = sensitive plaintext read, e.g. trace_payload)
	ResourceType string // provider | model | route | plugin | tenant | group | api_key | quota | trace_payload
	ResourceID   string
	// Tenant is the AFFECTED tenant's name (ADR-0019), or nil for a global/
	// platform-level action. It drives tenant-scoped audit reads: a tenant-admin
	// sees rows for its tenant (including super-admin actions on it).
	Tenant *string
	After  any
}

// Record appends an audit row. After is marshaled to JSONB (nil → NULL).
func (r *AuditRepo) Record(ctx context.Context, e AuditEntry) error {
	var after []byte
	if e.After != nil {
		b, err := json.Marshal(e.After)
		if err != nil {
			return err
		}
		after = b
	}
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO audit_logs (operator_id, action, resource_type, resource_id, tenant, after)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.OperatorID, e.Action, e.ResourceType, e.ResourceID, e.Tenant, nullableJSON(after),
	).Error
}

// nullableJSON returns nil for empty bytes so the column is NULL, else a
// json.RawMessage the pgx driver encodes as JSONB.
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}
