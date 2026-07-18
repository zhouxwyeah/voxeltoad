# ADR-0009: Request normalization layer (max_tokens default, system extraction, alternation)

- Status: Accepted
- Date: 2026-06-30

## Context

Adapters were defined as **pure translators** (ADR-0001/0002): format mapping
only, no semantic rewriting. Claude's Messages API breaks that assumption for an
OpenAI-compatible gateway, because a valid OpenAI request is not always a valid
Claude request:

- **max_tokens is required** by Claude (optional for OpenAI). A routed request
  lacking it 400s on Claude.
- **system prompt** must be a top-level field, not a `system`-role message.
- **strict user/assistant alternation**: Claude rejects consecutive same-role
  turns (OpenAI allows them); they must be merged.

Doing these inside `BuildRequest` would make adapters carry semantic rewriting
(e.g. merging messages changes request meaning), contradicting the pure-
translation contract.

## Decision

Introduce a **request normalization layer** that runs after routing (so the
target provider/model is known) and before the adapter's `BuildRequest`. The
adapter stays a pure translator of an already-normalized `UnifiedRequest`.

Normalization responsibilities:

1. **max_tokens default** — sourced from `Model` config (a new
   `Model.DefaultMaxTokens`), injected when the request omits it. Per-model and
   centrally tunable; the adapter never invents a value.
2. **system handling** — leave `system`-role messages in the unified request;
   the Claude adapter lifts them to the top-level `system` field at translation
   time (this is mechanical mapping, allowed). Normalization only needs to
   handle the multi-system / mid-conversation system case by concatenating into
   a single leading system (documented behavior).
3. **alternation merge** — consecutive same-role turns are merged (joined with
   "\n\n") into one, producing a strictly alternating sequence. This is the
   semantic rewrite kept OUT of adapters.

Normalization is provider-aware (driven by the resolved provider's adapter
type): OpenAI-targeted requests skip the Claude-specific rewrites.

## Consequences

- Adapters remain pure translators; the contract from ADR-0001/0002 stands
  unmodified. The "where does rewriting live" tension is resolved by a dedicated
  layer, not by weakening the adapter contract.
- `Model` config gains `DefaultMaxTokens`. (Schema addition in step 5.)
- Consecutive-user merge with "\n\n" is a documented, slightly lossy
  transformation; acceptable because the alternative (rejecting the request) is
  worse for the cross-provider promise.
- Multi-system concatenation is documented behavior, not an error, to maximize
  compatibility.
