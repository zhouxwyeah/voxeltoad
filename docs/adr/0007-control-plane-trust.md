# ADR-0007: Data-plane ↔ management-plane trust (shared secret)

- Status: Accepted (implemented step 4)
- Date: 2026-06-30

## Context

The data plane pulls dynamic config from the management plane's
`/internal/config/snapshot` endpoint. That payload includes sensitive material
(provider `APIKeyRef`s and, per ADR-0006, the data channel for key lookups).
Today the endpoint is unauthenticated: anyone able to reach the management
plane's port can pull the full configuration — an internal back door. Mutual
trust between the planes must be established as part of the auth work, not
deferred.

All three reference gateways gate their management surface (master key / root /
gateway admin); ours had no equivalent for the plane-to-plane channel.

## Decision

Authenticate the plane-to-plane channel with a **shared secret** carried in the
bootstrap configuration.

- A shared secret is configured on both planes via bootstrap config (resolved
  through the existing `config.ResolveSecret`, so it can be `env://...` and stay
  out of logs/snapshots).
- The data plane sends it (e.g. an `Authorization`/`X-Internal-Token` header) on
  every snapshot request; the management plane rejects requests lacking the
  correct secret with 401.
- This is independent of, and in addition to, client API-key auth (ADR-0006) and
  upstream credential resolution (ADR-0003).

## Consequences

- The config snapshot (and the secrets it references) is no longer an open
  internal endpoint.
- Operationally simple for the VM/traditional deployment stage (one shared
  secret per environment); no PKI to manage.
- Weaker than mutual TLS (no per-peer identity, rotation is manual). mTLS is the
  natural upgrade if/when the deployment moves to Kubernetes; recorded as a
  future option, not adopted now.
- The management plane's human-facing admin API (operator login, RBAC) is a
  separate concern handled in step 7; this ADR only covers the data↔management
  machine channel.
