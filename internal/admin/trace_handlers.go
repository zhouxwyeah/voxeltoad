package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"voxeltoad/internal/store"
)

// mountTrace wires the read-only LLM trace-payload feed (ADR-0039) for BOTH
// roles, mirroring mountRequestLogs (ADR-0021 §7). Super-admin sees all tenants;
// a tenant-admin is scoped to its own tenant. Scope is resolved from the
// operator, never the request, so a tenant-admin cannot read another tenant's
// payloads.
//
// trace_payloads carries prompt/completion PLAINTEXT, so it is more sensitive
// than request_logs: the detail read (GET .../requests/:request_id) is AUDITED
// (ADR-0039 §5) — reading plaintext is a sensitive operator action, unlike
// reading metadata. The list read (session summary, no bodies) is not audited.
func mountTrace(g *gin.RouterGroup, db *store.DB) {
	// Session trace: the chronological request list for a session, summary only
	// (no JSONB bodies). Pairs with GET /api/v1/request-logs/sessions/:session_id,
	// which gives the metadata timeline; this adds the message/raw drill-down.
	g.GET("/trace/sessions/:session_id", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		sessionID := c.Param("session_id")
		if sessionID == "" {
			badRequest(c, "session_id path parameter is required")
			return
		}
		repo := store.NewTracePayloadQueryRepo(db, tenant)
		rows, err := repo.ListBySession(c.Request.Context(), sessionID, parseLimit(c))
		if err != nil {
			internalErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"session_id": sessionID,
			"requests":   rows,
		})
	})

	// Single-request detail: the full message + raw layers. AUDITED — reading
	// prompt/completion plaintext is a sensitive operator action (ADR-0039 §5).
	g.GET("/trace/requests/:request_id", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		requestID := c.Param("request_id")
		if requestID == "" {
			badRequest(c, "request_id path parameter is required")
			return
		}
		repo := store.NewTracePayloadQueryRepo(db, tenant)
		detail, found, err := repo.GetByRequestID(c.Request.Context(), requestID)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
				"message": "trace payload not found for request_id " + requestID,
				"type":    "not_found",
			}})
			return
		}
		// Audit the plaintext read. Best-effort: an audit write failure must not
		// block the read (it is a side channel). resource_type "trace_payload" is
		// a new read-audit category; resource_id is the request_id read.
		op := operatorFrom(c)
		auditRepo := store.NewAuditRepo(db)
		_ = auditRepo.Record(c.Request.Context(), store.AuditEntry{
			OperatorID:   &op.ID,
			Action:       "read",
			ResourceType: "trace_payload",
			ResourceID:   requestID,
			Tenant:       strPtrOrNil(detail.Tenant),
		})
		c.JSON(http.StatusOK, detail)
	})

	// Single-row detail by primary key id: used when request_id is duplicated
	// across requests (some clients send the same X-Request-Id for every request
	// in a session). The row id is unique, so each request resolves to its own
	// payload. Also AUDITED like the request_id read (ADR-0039 §5).
	g.GET("/trace/rows/:id", func(c *gin.Context) {
		tenant, ok := usageTenantScope(c, db)
		if !ok {
			return
		}
		rowIDStr := c.Param("id")
		rowID, err := strconv.ParseInt(rowIDStr, 10, 64)
		if err != nil || rowID <= 0 {
			badRequest(c, "id path parameter must be a positive integer")
			return
		}
		repo := store.NewTracePayloadQueryRepo(db, tenant)
		detail, found, err := repo.GetByRowID(c.Request.Context(), rowID)
		if err != nil {
			internalErr(c, err)
			return
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
				"message": "trace payload not found for id " + rowIDStr,
				"type":    "not_found",
			}})
			return
		}
		op := operatorFrom(c)
		auditRepo := store.NewAuditRepo(db)
		_ = auditRepo.Record(c.Request.Context(), store.AuditEntry{
			OperatorID:   &op.ID,
			Action:       "read",
			ResourceType: "trace_payload",
			ResourceID:   rowIDStr,
			Tenant:       strPtrOrNil(detail.Tenant),
		})
		c.JSON(http.StatusOK, detail)
	})
}

// strPtrOrNil returns a pointer to s when non-empty, else nil (for the audit
// entry's affected-tenant field).
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
