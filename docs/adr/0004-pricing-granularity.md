# ADR-0004: Pricing granularity

- Status: Resolved (superseded by ADR-0012; pricing is per-provider)
- Date: 2026-06-30

## Context

`Pricing` currently hangs off `Model` (keyed by alias). But one alias can route
to multiple providers (primary OpenAI, failover to Azure/another vendor) with
**different per-token prices**. The observability schema requires billing by the
**actually-hit provider** (`llm.provider`), so pricing keyed only by alias would
mis-bill after a failover.

## Decision

**Deferred.** Steps 2 (adapter) and 5 (routing/failover) do not touch billing,
so pricing granularity does not block them. The decision is recorded now so it
is not forgotten and is made when billing is implemented in step 6.

Leading candidate (not yet committed): keep `Pricing` on `Model` as the default,
but allow a per-provider override so a failover provider can carry its own
rate; bill using the rate of the provider actually hit.

## Consequences

- No schema change now; `Model.Pricing` stays as-is through steps 2–5.
- Step 6 must resolve this before computing cost, and must align with the
  `llm.provider` (actual hit) observability field to avoid mis-billing on
  failover.
