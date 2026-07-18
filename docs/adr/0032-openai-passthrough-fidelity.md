# ADR-0032: OpenAI→OpenAI fidelity — passthrough, raw Content, and SDK strategy

- Status: Accepted
- Date: 2026-07-08

## Context

The gateway's OpenAI→OpenAI data path (client ↔ gateway ↔ OpenAI-compatible
upstream) suffered from systemic data loss. Three independent mechanisms
conspired to drop fields that are valid in the OpenAI wire protocol but have no
counterpart in our hand-written `UnifiedRequest`/`UnifiedResponse`/`Chunk`
structs:

1. **Request decode unconditionally discards unknown fields.** The HTTP handler
   (router.go) used `json.NewDecoder(r.Body).Decode(&wire)` into a struct
   embedding `UnifiedRequest`. `Extra` was `map[string]any` tagged `json:"-"`,
   so `response_format`, `n`, `seed`, `stop`, `top_p`, `parallel_tool_calls`,
   etc. were silently dropped on the floor.

2. **Response and stream chunk re-encode trims extra fields.** Non-streaming
   responses went through `ParseResponse` (direct unmarshal into
   `UnifiedResponse`), then `router.go` re-marshalled and wrote the result —
   losing `system_fingerprint`, `logprobs`, multi-choice `choices[1:]`, and
   provider-specific extensions. Streaming chunks went through `toWireChunk`
   re-encode with the same effect.

3. **`Message.Content` being a plain `string` cannot represent `null` or
   multipart arrays.** Assistant messages with `tool_calls` carry
   `content: null` in the OpenAI spec; our `string` field rendered this as
   `"content":""` — a protocol violation. Multipart content
   (`[{type:text,...},{type:image_url,...}]`) was impossible to express.

These issues were discovered through repeated tool-calling compatibility bugs
during testing of OpenAI-compatible upstreams (Hunyuan, Zhipu, etc.).

## Decision

Three interrelated changes, all scoped to the **OpenAI→OpenAI path only**
(Claude/Anthropic cross-protocol translation is deferred):

### 1. Request: structured Extra with custom UnmarshalJSON/MarshalJSON

`UnifiedRequest.Extra` is changed from `map[string]any` (`json:"-"`) to
`map[string]json.RawMessage`. Two custom methods are added:

- **`UnmarshalJSON`**: unmarshals all fields into a `map[string]json.RawMessage`,
  extracts known fields (model, messages, stream, temperature, max_tokens,
  tools, tool_choice) into struct members, then stores the remainder in `Extra`.
  Unknown fields are preserved byte-for-byte.

- **`MarshalJSON`**: marshals known fields into a map, then overlays `Extra`
  entries — known fields take priority, so an Extra entry whose key collides
  with a known field is silently dropped.

The HTTP handler (`router.go`) switches from `json.NewDecoder(r.Body).Decode()`
to `io.ReadAll` + two-step decode: `UnifiedRequest` (for the request body) and a
small `affinity` struct (for `user`/`prompt_cache_key`, avoiding a Go promoted
`UnmarshalJSON` shadowing issue).

The OpenAI adapter's `BuildRequest` merges `Extra` into the upstream body after
marshalling the known `wireRequest` fields.

**Rejected alternative**: raw body byte-level overwrite. This would bypass
`normalize.Apply`'s message rewriting and plugin-side `Content` mutations (e.g.,
PII redact), contradicting ADR-0009's pipeline contract.

### 2. Response: raw near-passthrough via `Raw` fields

Two non-destructive `json:"-"` fields are added to shared types:

- **`UnifiedResponse.Raw json.RawMessage`**: set by `ParseResponse` to the
  original upstream body. `router.go` prefers `resp.Raw` over
  `json.Marshal(resp)` when writing the client response.

- **`Chunk.Raw json.RawMessage`**: set by `streamReader.Recv` to the original
  SSE data line. `stream.go` prefers `chunk.Raw` over `toWireChunk(chunk)`
  when emitting an event.

Both fields are non-destructive: Claude adapter paths leave them `nil` and
continue to use the existing `toWireChunk` / `json.Marshal` re-encode path.
`ExtractUsage` continues to work because `Usage` is still populated alongside
`Raw`.

This design choice eliminates the re-encode data loss for OpenAI→OpenAI while
requiring **zero changes** to Claude adapter and preserving the `Usage`
extraction contract for billing.

### 3. Content: raw-backed custom type

`Message.Content` is changed from `string` to a custom `Content` type backed by
`json.RawMessage`. The type preserves the original JSON wire form (string,
`null`, or array) byte-for-byte:

- `UnmarshalJSON` / `MarshalJSON` store and return raw bytes without
  interpretation.
- `Text() string`: returns the decoded string value (or concatenated text parts
  for multipart arrays).
- `SetText(s string)`: replaces content with a JSON string.
- `IsNull() bool` / `IsMultipart() bool`: shape query methods.

This change touches all code that reads or writes `Message.Content`:
normalization (`mergeConsecutive`, `collapseSystem`), plugins (PII,
injection, sensitive, moderation), Claude adapter, and session-affinity
prefix hashing. All migrate to use `Content.Text()` / `Content.SetText()`
accessors.

### 4. SDK / framework strategy

**We do not adopt a gateway framework or a runtime SDK dependency.** The
rationale:

- **Gateway frameworks** (one-api, LiteLLM, new-api, etc.) are full
  applications, not libraries. The project already has billing, quota, RBAC,
  plug-in chains, and observability (30+ ADRs); swapping in a framework
  would require a rewrite or introduce a dual-system conflict.

- **Official SDKs as runtime wire types** (openai-go, anthropic-sdk-go) are
  not suitable. `openai/openai-go` uses `param.Opt` generics unsuitable for
  wire structs. `sashabaranov/go-openai` (plain structs) still cannot cover
  provider-specific extension fields (Hunyuan, Zhipu, etc.) — passthrough
  naturally covers these for free. Adopting a full SDK type model fights
  against the passthrough philosophy and regresses to the "model every field"
  approach that caused the original data loss.

- **Test-only SDK oracle**: we do adopt `github.com/sashabaranov/go-openai`
  as a **test-only dependency** (never imported by production code). Four
  conformance tests validate that our output bytes unmarshal correctly into
  the official SDK types (`ChatCompletionRequest`, `ChatCompletionResponse`,
  `ChatCompletionStreamResponse`). This acts as a drift detector: if OpenAI
  introduces, renames, or deprecates a field, the SDK-level unmarshal catches
  discrepancies between our adapter output and the authoritative schema.

### 5. Multi-choice (n>1) — deferred

The `Chunk` struct is shared by both OpenAI and Claude adapters. Converting it
to a multi-choice model would force Claude adapter changes — violating the
"Claude is postponed" scope constraint. Instead, the chunk-level near-passthrough
(`Chunk.Raw`) covers n>1 for OpenAI→OpenAI automatically: the raw SSE data line
includes all choices. Structured multi-choice `Chunk` support is left as an
independent future item.

## Consequences

- **No field is silently dropped on OpenAI→OpenAI paths.** `response_format`,
  `n`, `stop`, `seed`, `top_p`, `logprobs`, `parallel_tool_calls`,
  `system_fingerprint`, and any provider-specific extension fields all survive
  the round-trip.

- **Assistant `content: null` with `tool_calls` is now protocol-correct.**
  The `Content` type emits `null` (not `""`) for zero-value content, matching
  the OpenAI spec.

- **Multipart content is forward-compatible.** Though not actively used, the
  `Content` type preserves `[{type:text,...},{type:image_url,...}]` arrays
  through JSON round-trips without data loss.

- **`Chunk.Raw` and `UnifiedResponse.Raw` are additive, non-breaking fields.**
  Claude adapter paths continue to use the existing `toWireChunk` re-encode.
  No existing tests or production paths are affected.

- **SDK dependency is test-only.** `go.mod` gains `sashabaranov/go-openai` but
  the production binary does not link it. The conformance tests provide an
  authoritative schema oracle with zero runtime cost.

- **Content migration is pervasive but mechanical.** Every site that reads or
  writes `Message.Content` must use the accessor pattern
  (`Content.Text()` / `Content.SetText()`). This is a one-time cost paid now
  to eliminate the `string` → `Content` impedance mismatch permanently.

- **Claude cross-protocol tools support is explicitly deferred.** The
  `Content` type and `Raw` passthrough fields lay groundwork (preserving raw
  bytes, supporting null content), but the wire-level message block mapping
  (tool_calls → tool_use, tool → tool_result, content block arrays, streaming
  `content_block_start` / `input_json_delta`) remains future work.
