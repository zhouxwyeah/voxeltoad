# ADR-0010: Claude adapter — self-judged stream end, usage assembly

- Status: Accepted
- Date: 2026-06-30

## Context

The streaming contract was shaped around OpenAI in step 1/2: `pkg/sse.Done`
(the `[DONE]` sentinel) and the Chunk usage contract "usage arrives on the
trailing chunk". Claude's streaming differs:

- **No `[DONE]` sentinel.** Claude ends with a named event `message_stop`.
- **Named SSE events**: `message_start`, `content_block_delta` (text in
  `delta.text`), `message_delta` (carries `stop_reason` and output usage),
  `message_stop`.
- **Usage split across the stream**: `input_tokens` arrives early in
  `message_start`; `output_tokens` arrives late in `message_delta`. OpenAI, by
  contrast, delivers full usage in one trailing chunk.

This challenges two earlier decisions: `sse.Done` as a shared concept, and
"usage only on the trailing chunk".

## Decision

### Stream-end is adapter-judged, not a shared sentinel
`pkg/sse` stays protocol-agnostic: it decodes frames (including the `event:`
field) but does NOT own end-of-stream semantics. `sse.Done` remains only a
convenience constant for OpenAI-style adapters. Each adapter decides termination:
the OpenAI adapter on `data: [DONE]`, the Claude adapter on
`event: message_stop`. `StreamReader.Recv` returning `io.EOF` is the uniform
signal the proxy relies on, regardless of how the adapter detected the end.

### Claude adapter assembles usage to honor the Chunk contract
The Claude adapter buffers `input_tokens` from `message_start` internally and,
when it sees `message_delta` (output usage + stop_reason), emits a unified Chunk
whose `Usage` combines buffered input + output into
prompt/completion/total. Intermediate content chunks carry `Usage == nil`, so
the existing Chunk usage contract ("usage on the trailing chunk; nil before")
holds unchanged for downstream billing — the assembly is hidden inside the
adapter.

## Consequences

- The Chunk usage contract (ADR step-0) is preserved without modification;
  Claude's split usage is reconciled inside the adapter, invisible to proxy and
  billing.
- `pkg/sse` does not grow protocol-specific end-of-stream logic; it stays a pure
  framing codec. `sse.Done` is explicitly an OpenAI-family convenience, not a
  universal terminator.
- The Claude adapter is stateful within a single stream (buffers input_tokens);
  this state is per-StreamReader, so it is not shared and not a concurrency
  concern.
- If Claude emits an `error` event mid-stream, the adapter surfaces it as a
  non-EOF error from Recv, which the proxy already handles by terminating the
  client stream with `[DONE]` (step 3.5).
