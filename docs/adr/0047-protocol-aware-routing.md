# ADR-0047: Protocol-aware routing (implicit passthrough)

- Status: Accepted
- Date: 2026-07-22
- Builds on: [ADR-0032](0032-openai-passthrough-fidelity.md) (OpenAI→OpenAI passthrough), [ADR-0045](0045-anthropic-ingress-protocol.md) (Anthropic ingress)

## Context

ADR-0045 shipped Anthropic ingress (`/v1/messages`) with translation: an Anthropic-protocol client can drive an OpenAI-protocol upstream, and vice versa. The original follow-up plan proposed an explicit `passthroughFor` check in the router: when the hit provider's adapter matches the ingress protocol, relay the upstream bytes verbatim (no re-encode).

Review surfaced a more elegant direction: **make passthrough a natural consequence of routing, not a router-layer special case.** If the route selector prefers providers whose adapter speaks the same protocol as the ingress, the matched provider's `Raw` bytes are already in the client's protocol — the codec's existing Raw-priority (OpenAI already has it) makes passthrough happen implicitly. No new code path in the router.

## Decision

### Protocol-aware candidate reordering

The dispatcher, after `router.Candidates` returns the strategy-ordered candidate list, applies a **stable partition**: providers whose adapter matches the ingress protocol move to the front, preserving the strategy order within each group.

```
anthropic ingress + route [openai-p1, claude-p2, openai-p3]
  → strategy order (e.g. priority): [openai-p1, claude-p2, openai-p3]
  → protocol partition:             [claude-p2, openai-p1, openai-p3]
                                       ^^^^^^^^  ^^^^^^^^^^^^^^^^^^^^^^^^
                                       matched   failover (translated)
```

The ingress protocol is carried on the request context (`withIngressProtocol`). The dispatcher reads it in `Forward`/`ForwardStream` and calls `preferProtocol`. No-op when:
- the context carries no protocol (single-provider test mode, or pre-ADR-0045 code paths), or
- the dispatcher has no preparer (`NewSingleProviderDispatcher`, no `adapterByPvd` map).

### Naming reconciliation

Ingress protocols ("openai" / "anthropic", ADR-0045) and adapter names ("openai" / "claude", ADR-0001) have an asymmetric naming: `anthropic` ingress ↔ `claude` adapter. The mapping lives in one place — `ingress.Protocol.AdapterName()` — next to the enum, so adding a protocol forces the compiler to make you extend it.

### Codec Raw-priority (passthrough mechanism)

Both ingress codecs now prioritize `Raw` when non-empty:
- **OpenAI codec** (existing, ADR-0032): `EncodeResponse` returns `resp.Raw`; `streamEncoder.EncodeChunk` returns `chunk.Raw`.
- **Anthropic codec** (this ADR): same pattern added.

This is safe because protocol-aware routing guarantees: when the anthropic codec sees non-empty `Raw`, the hit provider's adapter is `claude` (same wire protocol). Protocol mismatch (anthropic ingress + openai adapter) leaves `Raw` empty (the openai adapter does preserve Raw, but the dispatcher's `preferProtocol` only promotes claude providers for anthropic ingress; if failover lands on an openai provider, the anthropic codec still has `Raw` from... wait, the openai adapter DOES set Raw). 

**Correction**: the openai adapter always sets `resp.Raw`. So when anthropic ingress fails over to an openai provider, `resp.Raw` is non-empty but holds **OpenAI-format** bytes. The anthropic codec's Raw-priority would incorrectly relay OpenAI bytes.

**Resolution**: the anthropic codec's Raw-priority is gated on the Raw being claude-format. But the codec doesn't know the adapter. Two options:
1. Add a `RawProtocol` field to `UnifiedResponse` / `Chunk` so the codec can check.
2. Trust the protocol-aware routing: anthropic ingress prefers claude providers; failover to openai is the degraded path.

Option 2 is unsafe (failover exists). **Option 1 is correct**: `UnifiedResponse.RawProtocol` and `Chunk.RawProtocol` record which adapter produced the Raw. The codec only uses Raw when `RawProtocol` matches its own protocol family. This is a small addition to the existing Raw fields.

### `Chunk.Raw` / `UnifiedResponse.Raw` contract

- **Raw** = the upstream's response bytes in the **adapter's native protocol** (OpenAI JSON for openai adapter; Anthropic JSON/SSE for claude adapter).
- **RawProtocol** = the protocol of those bytes (`""` = unknown/none, `"openai"`, `"anthropic"`). Set by the adapter alongside Raw.
- The ingress codec uses Raw only when `RawProtocol.AdapterName() == codec.Protocol().AdapterName()` (same wire family).

### Claude adapter: preserve Raw

`ParseResponse` now sets `Raw: body` and `RawProtocol: "anthropic"` (the claude adapter speaks Anthropic wire). `ParseStream`'s `Recv` reassembles complete SSE frames (`event: X\ndata: Y\n\n`) into `Chunk.Raw` (with `RawProtocol: "anthropic"`), because `pkg/sse.Decoder` discards the original frame bytes. This reassembly is the claude adapter's responsibility — the OpenAI adapter stores only `ev.Data` (its codec re-wraps it as `data: ...`); the asymmetry is documented in this ADR.

### Interaction with routing strategies

The stable partition preserves strategy order **within** each protocol group. Cross-group order is overridden by protocol match:

| strategy | matched group | cross-group |
|---|---|---|
| priority | config order | matched group first |
| round_robin | rotates within group | cross-group load skew (accepted) |
| weighted | weighted within group | cross-group skew (accepted) |
| session_affinity | HRW within group | cross-protocol sessions (rare) may switch groups |

**Known trade-off**: protocol-match priority means cross-group load balancing is intentionally sacrificed for passthrough fidelity. A single claude provider receives all anthropic traffic even if openai providers are idle. This is the correct default — passthrough preserves provider-specific fields and avoids re-encode bugs. Operators needing cross-group balance should add more providers to the matched group (e.g. a second claude provider).

### Failover semantics

1. Prefer protocol-matched providers (passthrough).
2. If all matched providers fail (retryable), fail over to the next candidate — which is protocol-mismatched (translated).
3. The codec's Raw-gating (via `RawProtocol`) ensures mismatched Raw is ignored and the response is properly re-encoded.

This is **graceful degradation**: best case is passthrough (fast, lossless), worst case degrades to the translation path (functional, slightly slower).

## Consequences

- `Dispatcher.preferProtocol` — one new method, O(n) partition on a small candidate list.
- `ingress.Protocol.AdapterName()` — naming reconciliation in one place.
- `UnifiedResponse.RawProtocol` / `Chunk.RawProtocol` — new fields gating Raw usage.
- Claude adapter preserves Raw (ParseResponse + ParseStream).
- Anthropic codec adds Raw-priority gated on RawProtocol.
- No router-layer passthrough judgment — the router is unchanged.
- ADR-0045's "no passthrough for anthropic↔claude" gap is closed implicitly.

## Alternatives considered

- **Explicit `passthroughFor` in router** (original plan). Rejected: adds a special case to the router, duplicates the protocol-matching logic, and is less observable to operators (the route config doesn't reveal which providers will passthrough).
- **Per-model `serving_protocols` field** (original plan Slice 3). Rejected: protocol-aware routing makes this redundant — the route's provider list + each provider's adapter already express "which protocols this model can serve natively."
