# ADR-0003: APIKeyRef secret resolution (env/plain + extension point)

- Status: Accepted
- Date: 2026-06-30

## Context

`Provider.APIKeyRef` was documented as "resolved from secret storage", but no
secret storage, env convention, or resolver existed. The OpenAI adapter (step 2)
must inject `Authorization: Bearer <key>` upstream, so it needs a concrete way
to turn a ref into a plaintext key — without blocking on a full secrets backend,
and without hard-coding plaintext keys into config.

## Decision

Define a small `SecretResolver` in `internal/config` with two built-in schemes
and a registration extension point:

- `env://VAR_NAME` — value of the named environment variable (**recommended**
  for real deployments).
- `plain://literal` or a bare literal (no `://`) — used verbatim (**dev/test
  only**).
- `RegisterSecretScheme(scheme, fn)` — extension point for future backends
  (e.g. `vault://`, `kms://`).

A ref containing `://` with an **unregistered** scheme is a hard error, so a
typo fails loudly instead of being silently treated as a literal key.

## Consequences

- Step 2 can inject credentials immediately via `ResolveSecret(provider.APIKeyRef)`.
- Production avoids plaintext-in-config by using `env://`; secrets stay out of
  the config snapshot and out of logs (resolved value is never logged — see
  design/observability.md).
- A real secrets manager is added later by registering a scheme, with no change
  to call sites or the config schema.
