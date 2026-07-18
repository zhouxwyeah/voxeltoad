# ADR-0028: CSV export endpoints — synchronous, limited-cap

- Status: Accepted
- Date: 2026-07-04
- Builds on: ADR-0019 (control-plane read APIs)

## Context

Operators need to export request logs, usage records, and audit logs for
compliance and offline analysis. The plan's C3 originally called for asynchronous
export with a background pipeline, which would require leader election (A4).
Given the low data volumes expected and the desire to avoid introducing a job
pipeline, a synchronous export with a row cap is preferred.

## Decision

### Query parameter: `?format=csv`

3 existing GET endpoints accept `?format=csv`:

| Endpoint | Export function | Row cap |
|----------|----------------|---------|
| `GET /api/v1/request-logs` | `exportRequestLogsCSV` | 2000 |
| `GET /api/v1/usage` | `exportUsageCSV` | 2000 |
| `GET /api/v1/audit` | `exportAuditCSV` | 2000 |

### Implementation

- All 3 endpoints reuse existing `filter` + `cursor` / `limit` logic
- CSV path bypasses pagination and uses a fixed `limit=2000`
- `Content-Disposition: attachment; filename=xxx.csv`
- `Content-Type: text/csv; charset=utf-8`
- **UTF-8 BOM** (`0xEF 0xBB 0xBF`) prepended so Excel/Microsoft tools
  interpret the file correctly
- Uses Go's `encoding/csv` writer for RFC 4180 compliance

### Shared utility: `internal/admin/csv.go`

A single `writeCSV(c *gin.Context, filename string, headers []string, rows [][]string)`
function serves all 3 endpoints. Row serialization is endpoint-specific
(e.g., `AuditRow.After` JSONB → JSON string for CSV safety).

### Row cap rationale

2000 rows is an arbitrary generous cap that prevents OOM from unbounded
exports without introducing pagination. Larger exports requiring filtering
beyond the cap should use the JSON API with cursor pagination and
client-side aggregation.

## Consequences

### Positive

- Zero new infrastructure — no job queue, no leader election (A4 not needed).
- Operators can get CSV data without writing scripts against the JSON API.
- Excel-compatible out of the box (BOM prevents CJK encoding issues).

### Negative

- 2000-row cap means large-compliance exports require multiple calls with
  filters or an alternative pipeline later.
- Synchronous HTTP response holds the connection while CSV is built and
  written — acceptable for 2000 rows (~100 KB), not scalable beyond ~10-50K.
- `AuditRow.After` (JSONB `[]byte`) is serialized as raw JSON in CSV — quoted
  commas in the JSON may confuse naive CSV parsers.

### Limitations

- No streaming — entire result set is built in memory before writing.
- Only 3 endpoints; `GET /api/v1/usage/summary` does not support CSV (deferred).

## Related

- ADR-0019: control-plane read API patterns
- ADR-0021: request_logs schema
- Plan C3: compliance export
