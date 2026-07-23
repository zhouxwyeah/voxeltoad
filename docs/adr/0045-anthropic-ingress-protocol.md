# ADR-0045: Anthropic ingress protocol (`/v1/messages`)

- Status: Accepted
- Date: 2026-07-20
- Supersedes: none (extends [ADR-0032](0032-openai-passthrough-fidelity.md) §5 deferred work, inbound direction)

## Context

The gateway's inbound wire shape was historically implicit: every request was assumed to be OpenAI-compatible (`POST /v1/chat/completions`). This was baked into the handler (`internal/proxy/router.go::chatCompletionsHandler` directly `json.Unmarshal`-ed the body, the stream writer emitted `chat.completion.chunk` with `[DONE]`, and the error response was always `{"error":{...}}`).

A driving use case surfaced: **Claude Code (which only speaks the Anthropic Messages API) needs to drive OpenAI-protocol upstreams through the gateway.** Without inbound Anthropic support, users had to either abandon Claude Code or point it at a separate Anthropic-only gateway. The capability is also increasingly requested by enterprises wanting one ingress shape for all Anthropic-shaped clients regardless of which upstream serves the request.

This is the first non-OpenAI inbound protocol in the codebase. The decision is not just "add a new endpoint" but "introduce the abstraction that makes inbound-protocol plurality possible without rewriting the data plane for each addition."

## Decision

### 1. Introduce an `internal/ingress/` layer (L2, peer of `internal/adapter/`)

Ingress Codec is the **dual** of Adapter: adapters translate unified → upstream-provider-native on the outbound side; codecs translate client-wire ↔ unified on the inbound side. Both are pure translators (values-in/values-out, no HTTP transport), mirroring ADR-0002's "adapter is a pure translator" philosophy extended to the ingress side.

The `Codec` interface (`internal/ingress/ingress.go`) carries five methods: `DecodeRequest`, `EncodeResponse`, `NewStreamEncoder`, `EncodeError`, plus `StreamContentType` and `StreamTerminator`. The `StreamEncoder` is a stateful per-stream translator (`EncodeChunk` + `Close`) because Anthropic's stream framing is block-oriented (each content element wrapped in `content_block_start` → `content_block_delta(s)` → `content_block_stop`) whereas OpenAI's is flat per-chunk deltas — the encoder must track Anthropic block indices across chunks.

Two implementations ship:
- `internal/ingress/openai/` — nearly identity (the unified model is already OpenAI-shaped). Exists so the proxy never hardcodes "inbound = OpenAI" again.
- `internal/ingress/anthropic/` — the actual protocol translation work.

The OpenAI wire types (`wireStreamChunk`, `toWireChunk`) were **moved out of `internal/proxy/`** into `internal/ingress/openai/` as part of this ADR. The proxy layer no longer owns any inbound wire shape.

### 2. Handler organization: one protocol-agnostic `serveChat`, two thin wrappers

`chatCompletionsHandler` and `messagesHandler` are each ~5 lines: they call `serveChat` with a specific codec. Everything else (agent detection, session extraction, normalize, plugin chain Pre/Post, dispatcher Forward/ForwardStream, telemetry/audit/billing) is identical across protocols and lives in `serveChat`. This is critical: a new ingress protocol must not require duplicating telemetry/audit/billing logic (which would inevitably drift).

### 3. Tool-use mapping (the bulk of the work)

Scope: `tools`/`tool_use`/`tool_result` ↔ `tools`/`tool_calls`/`role=tool` is fully supported in both decode and encode, streaming and non-streaming. This delivers the deferred `tool_use` mapping referenced in [ADR-0032](0032-openai-passthrough-fidelity.md) §5 — **but only in the ingress direction** (the Claude *outbound* adapter remains unchanged; it doesn't carry tools — see §gap).

The streaming state machine (`internal/ingress/anthropic/stream.go`) maintains:
- `textBlockOpen` + `textBlockIdx` — the text content block.
- `toolBlocks map[OpenAI_Index]Anthropic_blockIdx` — per-call state so parallel tool calls (interleaved OpenAI `Index` fragments) each get their own `content_block_start/input_json_delta/content_block_stop` lifecycle.
- `finished` — guards against double-emitting `message_delta` when both a finish-chunk and the encoder `Close()` could emit one.

`function.arguments` (OpenAI streaming) maps verbatim to `input_json_delta.partial_json` (Anthropic streaming). Both are incremental JSON text concatenated by the client; the translation is lossless.

### 4. Inbound protocol is global (no per-key/per-provider capability)

Any valid API key can use either ingress protocol. We do NOT add a per-key `allowed_inbound_protocols` field or a per-provider `inbound_protocol` declaration. Rationale:
- The data-plane capability dimension is set by [ADR-0033](0033-data-plane-keys-not-bound-to-roles.md); adding an inbound-protocol capability without clear demand is premature.
- Admin schema, frontend forms, and OpenAPI remain untouched (a deliberate scope reduction).
- If per-key protocol restriction is needed later it can be added as a new data-plane capability following ADR-0033's pattern.

### 5. Auth accepts both `Authorization: Bearer` and `x-api-key`

`bearerToken` accepts either header on every `/v1/` route. Claude Code defaults to `x-api-key`; configuring `ANTHROPIC_AUTH_TOKEN` makes it send `Bearer`. Both must work on both routes. Auth failures return the protocol-shaped envelope (selected by path via `codecForPath`) so e.g. Claude Code sees Anthropic-shaped 401s and OpenAI clients see OpenAI-shaped 401s.

## Gaps (deferred)

### input_tokens=0 in `message_start`

Anthropic's `message_start` reports `input_tokens`. OpenAI streaming only reports usage on the trailing chunk; we don't know `prompt_tokens` at `message_start` time. We emit `input_tokens: 0` in `message_start` and the full usage in `message_delta`. Billing takes usage from the unified model (accurate). The cosmetic gap is that Anthropic clients parsing `message_start` see 0; clients parsing the trailing `message_delta` see the right value.

Alternative considered: buffer the entire upstream stream before emitting `message_start` (would give accurate input_tokens). Rejected: it sacrifices TTFT (first-byte latency = full upstream latency), which is a worse trade for Claude Code-style interactive streaming. Re-open if a real client breaks.

### Image content blocks unsupported

Decode of `{type:image,...}` / `{type:image_url,...}` blocks returns an explicit error (`unsupported content type: image`). This is a clear failure rather than silent drop. Rationale: Anthropic's `source` format and OpenAI's `image_url` format are different, mapping is non-trivial, and Claude Code's primary payload is text + tool use. Re-open as separate ADR when needed.

### `cache_control` is dropped

Anthropic's `cache_control: {type:"ephemeral"}` (prompt cache hint) is silently ignored on decode. The upstream is OpenAI, which has its own automatic prompt cache that doesn't honor client hints; forwarding `cache_control` is a no-op. Adding it to the unified model would pollute every adapter (violates ADR-0032's "unified is OpenAI-shaped" invariant). If/when we do Anthropic inbound → Anthropic upstream, `cache_control` should travel via unified `Extra` and be emitted by the Claude outbound adapter (the deferred work in ADR-0032 §5).

### Message ID prefix

OpenAI `chatcmpl-...` IDs are forwarded as-is to the Anthropic response (no `msg_` prefix). Anthropic clients don't validate the ID prefix.

### `/v1/models` returns 501

Claude Code primarily uses configured models, not `/v1/models` discovery. The endpoint remains 501. If real clients break, a dedicated "list Anthropic-shape models from the model catalog" endpoint is a separate piece of work (model catalog is admin-plane data; the data plane has no model-list API today).

### Claude outbound adapter still doesn't support tools

ADR-0032 §5's deferred work on the Claude **outbound** adapter (`tool_use`/`content blocks`/`input_json_delta` on the unified → Anthropic-upstream side) is NOT addressed by this ADR. This ADR delivers only the **inbound** translation. If your upstream is Anthropic and you need tool support, that's still future work. (A future ADR-0045 inbound → ADR-0032 §5 outbound could close the loop on Anthropic inbound → Anthropic upstream passthrough — see plan §0.4 of the original design notes.)

## Consequences

- **New package**: `internal/ingress/` with subpackages `openai/` and `anthropic/`. Dependency rule: `internal/proxy/` → `internal/ingress/` → `internal/adapter/` (for unified types) + `pkg/sse/`. Two ingress implementations never import each other (mirrors ADR-0001 rule 2 for adapters).
- **Wire types move**: `wireStreamChunk`/`toWireChunk` left `internal/proxy/stream.go` for `internal/ingress/openai/`. Proxy no longer owns an inbound wire shape.
- **architecture.md / glossary.md / observability.md / CODEBUDDY.md** all updated (the design layer is the source of truth).
- **Three-entrypoint matrix**: `cmd/gateway`, `cmd/devstack`, `cmd/desktop` all gain `internal/ingress/` because they all assemble `proxy.Router` (the canary property from ADR-0041 is preserved).
- **Third ingress protocol** (e.g. Gemini): add `internal/ingress/<name>/`, register it, point its route at `serveChat` with that codec. No proxy / dispatcher / billing / telemetry changes needed.

## Alternatives considered

- **Duplicate the handler**: write a parallel `messagesHandler` that re-implements telemetry/audit/billing. Rejected: violates DRY and inevitably drifts (e.g. a future field added to the audit ledger for one protocol but not the other).
- **Make Adapter bidirectional** (add ingress methods to `internal/adapter.Adapter`): rejected because the two concepts are orthogonal — e.g. the Claude *outbound* adapter is totally unrelated to the OpenAI *inbound* codec. Bundling them makes the adapter interface sprawling and confuses "which side does this method belong to."
- **Byte-passthrough Anthropic→Anthropic** (like ADR-0032 OpenAI→OpenAI): out of scope; only Anthropic→OpenAI is in this ADR. Would be added by a future ADR alongside the Claude outbound adapter's deferred tool support.
