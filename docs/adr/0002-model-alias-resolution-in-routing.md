# ADR-0002: Model alias→upstream resolution happens in the routing layer

- Status: Accepted
- Date: 2026-06-30

## Context

"Model" was overloaded across the pipeline:

- `Model.Alias` — the name clients request (e.g. `default-chat`).
- `Model.UpstreamModel` — the provider-native name (e.g. `gpt-4o`).
- `UnifiedRequest.Model` — ambiguous: alias before routing, upstream after.

If an adapter reads `req.Model` while it still holds an alias, it would send a
name like `default-chat` upstream, which the provider does not recognize. The
"who turns alias into upstream model" step had no owner.

## Decision

The **routing layer** owns alias resolution. Concretely:

1. The client request enters with an **alias** (the public model name).
2. Routing looks up the `Route` for that alias, selects a `Provider`, and reads
   the corresponding `Model.UpstreamModel`.
3. Routing sets `UnifiedRequest.Model` to the **upstream model name** before the
   adapter's `BuildRequest` runs.

Adapters are pure translators: they receive an already-resolved upstream model
name and never see or resolve aliases. This is documented on
`UnifiedRequest.Model`.

## Consequences

- Adapters stay free of routing/config dependencies — preserves the
  pure-translation contract (and testability) from step 0.
- There is exactly one place that maps alias→upstream, easing debugging and
  observability (`llm.model.requested` = alias, `llm.model.resolved` = upstream).
- A single `UnifiedRequest.Model` field is reused (alias at ingress, upstream
  after routing). We accept the in-place mutation rather than carrying two
  fields, because the alias is still available via the observability attributes
  and the request's original payload. If this proves error-prone, revisit by
  splitting into `Alias` + `ResolvedModel`.
