# ADR-0006: API key authentication — hashed keys, cache-first data channel, public KeyID

- Status: Accepted
- Date: 2026-06-30

## Context

The data plane authenticates client requests by an API key the gateway issues
(distinct from upstream provider credentials — cf. ADR-0003 for the upstream
side). Three sub-decisions interact: how keys are stored, how the (stateless)
data plane obtains key data, and how a key is identified in logs/traces.

API keys differ fundamentally from the low-frequency dynamic config the snapshot
poller carries: keys can be numerous (potentially 10^5+), are read on every
request, and must take effect in near-real-time (a key created in the console
must work immediately). Forcing them through the full-snapshot channel (whole
JSON pulled and atomically swapped every poll interval) would bloat the
snapshot, add GC pressure, and impose up to a full poll-interval delay before a
new key works — the wrong tool for this access pattern.

## Decision

### Storage — hashed, never plaintext
API keys are stored only as hashes (e.g. SHA-256). Authentication hashes the
presented key and looks up the record. A leaked database does not leak usable
keys. (Matches LiteLLM/new-api.)

### Data channel — cache-first with snapshot fallback
The data plane resolves keys via a **local cache first**; on a miss it falls
back to the management plane / shared store, then caches the result with a short
TTL. This keeps keys real-time and avoids snapshot bloat, while not requiring
every request to hit the store. Negative results (unknown key) are also cached
briefly to blunt invalid-key floods. Revocation is handled by TTL expiry (and
may later be augmented by an explicit invalidation signal).

This introduces a read dependency for the data plane, which is acceptable:
authentication inherently needs key state, and the cache keeps the steady-state
path in-memory. It is a narrower dependency than making the whole data plane
stateful.

### Identification — public KeyID
Each key has an independent, public, human-readable identifier (e.g.
`key_01H...`) stored alongside its hash. Logs, traces, and audit use this KeyID
(`llm.api_key_id`); the plaintext key and its hash are never logged. This avoids
the collision/partial-plaintext-exposure problems of using a key prefix as the
identifier.

## Consequences

- New keys work within the cache TTL, not a poll interval.
- The snapshot channel stays small and reserved for genuine low-frequency
  config (providers/models/routes/plugins).
- Auth carries a key→record cache in the data plane; cache size/TTL are tunable.
- `llm.api_key_id` is populated with the public KeyID, enabling per-key audit
  without exposing secrets.
- Supersedes the earlier suggestion to ship keys inside the config snapshot.
