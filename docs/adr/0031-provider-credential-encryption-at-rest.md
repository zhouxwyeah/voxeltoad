# ADR-0031: Provider upstream credential encryption at rest

- Status: Accepted
- Date: 2026-07-05

## Context

ADR-0003 introduced `Provider.APIKeyRef` as a reference string resolved at
runtime, with only `env://` and `plain://` (dev/test) schemes implemented.
`design/database.md §5.5` explicitly deferred a provider-credential encryption
scheme. Before taking voxeltoad to production we must satisfy the
following operational requirements:

1. Upstream API keys (OpenAI, Anthropic, etc.) must **not** be stored as
   plaintext in the database, in config snapshots, or in any log/trace.
2. Operators must be able to rotate a provider credential through the admin
   API without redeploying the data plane or updating environment variables.
3. The data plane must be able to resolve a credential from the database on
   every dispatcher rebuild, while keeping the plaintext only in memory and
   never serializing it.
4. The change must preserve the existing `env://` production path and remain
   backward-compatible with existing provider rows.

The existing `RegisterSecretScheme` extension point makes it possible to add a
new resolver without changing the call sites that consume `ResolveSecret`.

## Decision

Introduce a dedicated `provider_credentials` table that stores encrypted
upstream API keys, plus a new `db://provider/<name>` secret scheme resolved by
the data plane.

### Storage model

- `providers.spec.api_key_ref` remains a **reference string** (never a plaintext
  key). It can be one of:
  - `env://VAR_NAME` — existing, recommended for deployments that inject secrets
    via environment.
  - `db://provider/<name>` — new, references the encrypted credential stored in
    `provider_credentials`.
  - `plain://literal` or bare literal — dev/test only, unchanged.
- A new table `provider_credentials` stores the encrypted credential:
  - `provider_name VARCHAR PRIMARY KEY` (matches `providers.name`).
  - `ciphertext BYTEA NOT NULL`.
  - `nonce BYTEA NOT NULL`.
  - `algorithm VARCHAR NOT NULL` (e.g. `AES-256-GCM`).
  - `key_version VARCHAR NOT NULL` (for KEK rotation).
  - `created_at / updated_at TIMESTAMPTZ`.
- When a provider is deleted, its credential row is deleted (cascade via SQL or
  explicit cleanup in the repository). When a provider is updated, the
  credential is written only if the operator supplies a new plaintext key.

### Encryption design

- P0 uses a single data-encryption key (DEK) derived from a base64-encoded
  32-byte master key supplied via the environment variable
  `GATEWAY_PROVIDER_CREDENTIAL_KEK`.
- Algorithm: `AES-256-GCM` with a random 12-byte nonce per encryption.
- The `key_version` column is reserved for KEK rotation; v0 means the current
  process key. Future work can support a key-encryption-key service
  (Vault/KMS) without changing the table shape.
- Decryption happens in the data plane (or admin credential endpoint) only;
  encrypted bytes never leave the database or are exposed through APIs.

### Secret scheme resolution

- The `internal/config` package exposes a `RegisterSecretScheme` extension,
  which is already the ADR-0003 mechanism.
- The data-plane composition root (`cmd/gateway`) registers a `db` scheme
  resolver that:
  1. Parses the reference as `db://provider/<provider_name>`.
  2. Loads the encrypted credential from the `CredentialRepo` backed by the
     same PostgreSQL connection used for quota.
  3. Decrypts it with the configured `credential.Service`.
  4. Returns the plaintext key to `proxy.BuildDispatcher`.
- If resolution fails, the dispatcher rebuild fails and the data plane keeps
  the last-good dispatcher (hot-reload safety). On first startup a failure is
  fatal, matching the current fail-fast behavior for unreachable config.

### Admin API security

- `GET /api/v1/providers` and `GET /api/v1/providers/{name}` do **not**
  return the raw `api_key_ref`. They return a masked form:
  - `env://***` for environment references.
  - `db://provider/<name>` for database references.
  - A sentinel value for dev/test references.
- A new dedicated endpoint `PATCH /api/v1/providers/{name}/credential` accepts a
  plaintext `api_key` or an external `api_key_ref` and updates the stored
  credential accordingly. The response never contains the plaintext key.
- Credential mutations are written to `audit_logs` with `before/after` metadata
  limited to scheme, key_version, and whether a credential existed. The
  plaintext and ciphertext are never logged.

### Frontend behavior

- Provider list shows the credential scheme and presence status, never the key.
- Provider create/edit form separates “endpoint configuration” from
  “credential update”. The credential input is write-only and masked.
- After a successful credential update the UI shows a confirmation message, not
  the secret.

## Consequences

- `provider_credentials` becomes the authoritative encrypted store for upstream
  API keys. The config snapshot still only carries a reference, so snapshot
  history remains safe even if a historical snapshot is exposed.
- `env://` remains fully supported; operators can migrate from `env://` to
  `db://provider/<name>` without reconfiguring the provider identity.
- `plain://` and bare literals remain dev-only and can be rejected in
  production via an opt-in flag in the future.
- Plaintext keys still exist in process memory while the dispatcher is active.
  This is unavoidable for upstream authorization, but mitigated by the
  existing observability rule that secrets are never logged (see
  design/observability.md).
- The data plane gains one more read-only dependency on PostgreSQL at startup
  and on every dispatcher rebuild. This is acceptable because the data plane
  already depends on PostgreSQL for quota.

## Related documents

- ADR-0003: `APIKeyRef` secret resolution extension point.
- ADR-0014: management-plane schema and soft-reference design.
- ADR-0017: operator RBAC and audit-log scoping.
- design/database.md §5.5: `provider_credentials` table (implemented per this ADR).
- design/observability.md: secret-logging prohibition.
