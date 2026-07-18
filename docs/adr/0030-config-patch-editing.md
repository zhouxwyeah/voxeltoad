# ADR-0030: Config PATCH editing via SELECT FOR UPDATE

- Status: Accepted
- Date: 2026-07-06
- Builds on: ADR-0014 (management-plane schema, spec JSONB), ADR-0019 (control-plane API style)

## Context

The config API (providers/models/routes/plugins) has long been POST-upsert only
(see `design/domain-flows.md:150` and `design/frontend.md:153-160`). "Editing"
an existing resource required re-submitting the entire object: the client had to
GET the full record, change one field, then POST all fields back. This has two
problems:

1. **Lost update on concurrent edits.** Two operators editing different fields
   of the same provider race; whoever POSTs last overwrites the other's change
   wholesale (last-write-wins, no conflict detection).
2. **Accidental field clobbering.** If the client omits a field in the POST
   (e.g. `timeouts`), the server unmarshals the Go zero value and persists it,
   silently wiping the previous setting.

What we need is a partial-update semantic: send only the changed fields, leave
the rest untouched.

## Decision

### PATCH endpoint with SELECT ... FOR UPDATE

Add a `PATCH /api/v1/{resource}/{name}` operation (starting with providers as a
pilot). The handler binds a `ProviderPatch` struct whose fields are pointers
(`*string`, `*int`, ...): a `nil` field means "leave unchanged", a non-nil
pointer means "overwrite". The store layer applies the patch inside a
transaction that issues `SELECT spec::text FROM providers WHERE name = ? FOR
UPDATE` before read-modify-write. The row lock serializes concurrent patches to
the same resource; cross-resource patches proceed in parallel.

### Why not optimistic concurrency control (version column / If-Match)

OCC would require a new `version` column on each config table (new migration),
returning it in every response, and a retry loop in the client on 409. That is
the right design for a high-contention hot path. Config writes are none of
those: they are low-frequency operator actions (a handful per day), performed
through the admin console by one or two operators. Pessimistic locking with
`FOR UPDATE` is simpler (no schema change, no retry contract) and entirely
adequate at this write volume.

### Why not keep POST-upsert

POST-upsert remains the right primitive for *create* and for *full-replace*
flows. PATCH complements it for partial edits. Both coexist.

## Consequences

### Positive

- No schema migration needed — `FOR UPDATE` works on the existing rows.
- Concurrent edits to the same provider can no longer silently lose updates.
- Clients can update a single field without re-submitting the whole record.
- Reuses the existing `auditMutation` middleware and `bumpGeneration` plumbing.

### Negative

- `FOR UPDATE` holds the row lock until the transaction commits. In practice
  the critical section is a few milliseconds (read spec, marshal, update), so
  lock contention is negligible at this scale.
- The pointer-field patch struct (`ProviderPatch`) is a new type per resource
  that must stay in sync with the config schema. This is a small maintenance
  cost; the pilot covers providers, and model/route/plugin follow the same shape.
- This is pessimistic, not optimistic: a long-running patch transaction could
  block another. Acceptable for operator-driven config; would need revisiting if
  config writes ever became automated/high-frequency.

## Scope

This ADR covers all four config resources: provider (initial pilot), model,
route, and plugin. All four follow the same SELECT ... FOR UPDATE pattern. The
handler layer validates the merged value (current spec + patch) before writing,
reusing each resource's existing cross-reference checks:

- **provider**: adapter must be in the registry
- **model**: upstream providers must exist; updated upstreams must not orphan an
  existing route (Route.Providers ⊆ Model.Upstreams)
- **route**: strategy enum; providers must exist and be upstreams of the model
- **plugin**: phase enum; (name, scope) is the composite identity — scope is
  passed as a query parameter and is NOT patchable

Plugin PATCH uses `UPDATE ... WHERE name=? AND scope=?` rather than the upsert
path's `DELETE + INSERT`, to preserve the row id and hold a row lock.

## Related

- ADR-0014: management-plane schema (identity columns + spec JSONB)
- ADR-0015: migration & snapshot versioning (`bumpGeneration` on every write)
- ADR-0019: control-plane read APIs (error envelope, cursor style this PATCH follows)
- `design/domain-flows.md:150` (the registered P2 gap this closes)
