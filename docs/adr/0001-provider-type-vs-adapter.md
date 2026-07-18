# ADR-0001: Provider brand (Type) is separate from protocol Adapter

- Status: Accepted
- Date: 2026-06-30

## Context

A `Provider` needs to express two things that were initially conflated into one
`Type` field: the **brand** of the upstream (OpenAI, Tencent Hunyuan, Zhipu,
Anthropic…) and the **wire protocol** it speaks. Most Chinese providers and
"OpenAI-compatible" endpoints speak the OpenAI protocol, so they should reuse a
single `OpenAICompatibleAdapter`; only protocols that genuinely differ (Claude)
need their own adapter.

With a single `Type` field, the adapter registry lookup was ambiguous: would
`registry.New("tencent")` need a Tencent adapter (contradicting reuse), or is
`tencent` just a label?

## Decision

Split the concepts into two fields on `Provider`:

- `Type` — the **brand**, descriptive and observability-facing (e.g. `tencent`,
  `zhipu`, `openai`, `anthropic`). Does not select behavior.
- `Adapter` — the **protocol adapter key** used for `registry.New(...)`. Only a
  small closed set exists: `openai` (shared by openai/tencent/zhipu/compatible)
  and `claude`.

## Consequences

- Adapter registry stays small; adding a new OpenAI-compatible brand requires
  **zero new Go code** — just a Provider config with `adapter: "openai"` and a
  distinct `base_url`/credentials.
- `Type` feeds the `llm.provider`/brand dimension in observability without
  affecting routing or adapter selection.
- Two fields can drift (a config could set `type: claude, adapter: openai`).
  Mitigation: admin-side validation (step 7) should warn on known mismatches;
  the data plane trusts `Adapter` for behavior.
