# ADR-0049: Multi-endpoint Provider — protocol-aware routing at endpoint granularity

- Status: Accepted
- Date: 2026-07-22
- Supersedes: [ADR-0047](0047-protocol-aware-routing.md)
- Builds on: [ADR-0001](0001-provider-type-vs-adapter.md) (Type/Adapter separation), [ADR-0045](0045-anthropic-ingress-protocol.md) (Anthropic ingress), [ADR-0047](0047-protocol-aware-routing.md) (protocol-aware routing — superseded)

## Context

Many providers (OpenAI, DeepSeek, Aliyun Bailian, Mistral, ...) expose BOTH OpenAI-compatible and Anthropic-compatible endpoints. ADR-0047 modeled this as two separate Provider records and partitioned across them at the router. This has three problems:

1. **Credential duplication** — the same upstream API key must be stored twice (one per provider), and rotating it means two operations.
2. **Operational noise** — billing/audit/health split one vendor into two providers, obscuring per-vendor cost and status.
3. **Cross-protocol failover degrades to translation** — when the claude-adapter provider fails, the anthropic client fails over to the openai-adapter provider and the response is translated (re-encoded), losing provider-specific fields even though the same vendor could have served the anthropic protocol natively on its other endpoint.

The project is pre-launch — no migration burden. We can reshape the Provider model and spec JSONB directly.

## Decision

### Provider carries multiple endpoints

`Provider.Endpoints []ProviderEndpoint` replaces the scalar `Adapter` + `BaseURL`. Each endpoint is one `(ID, Adapter, BaseURL, Timeouts?)` pair. At least one endpoint is required.

```go
type Provider struct {
    Name      string             // unique provider identifier
    Type      string             // brand (descriptive only, ADR-0001)
    Endpoints []ProviderEndpoint // >= 1; first is "primary"
    APIKeyRef string             // SHARED credential for all endpoints
    Timeouts  ProviderTimeouts   // provider-level default
    Weight    int
}

type ProviderEndpoint struct {
    ID       string              // optional; derived from adapter when empty
    Adapter  string              // "openai" | "claude"
    BaseURL  string
    Timeouts *ProviderTimeouts   // optional override
}
```

### Credential stays at provider level

`api_key_ref` is resolved once per provider in `BuildDispatcher` and reused for every endpoint adapter. The `provider_credentials` table (PK = `provider_name`) and `db://provider/<name>` scheme are unchanged. Endpoints have no credential field.

### Circuit-breaker keyed by (provider, endpoint)

`EndpointKey{Provider, Endpoint}` is the breaker key. A dual-protocol vendor's openai endpoint can trip independently of its anthropic endpoint — correct for independent deployment topologies.

### Protocol-aware endpoint selection (replaces ADR-0047's provider partition)

ADR-0047 partitioned candidates **across providers** by protocol. ADR-0049 partitions **within each provider**: for each candidate provider, `expandCandidates` picks the endpoint whose adapter matches the ingress protocol, falling back to the primary (first) endpoint. Provider order remains strategy-driven (ADR-0011); there is NO cross-provider protocol partition anymore because each multi-endpoint provider natively speaks the client's protocol via its matching endpoint.

```
anthropic ingress + route [dual-vendor(endpoints: openai, anthropic)]
  → expandCandidates: dual-vendor/anthropic  (protocol-matched endpoint)
  → passthrough: claude adapter's Raw relayed verbatim

anthropic ingress + route [openai-only(endpoints: openai)]
  → expandCandidates: openai-only/openai    (primary fallback, no match)
  → translation: anthropic codec re-encodes (RawProtocol gating prevents raw leak)
```

### `RawProtocol` gating (from ADR-0047, retained)

The `UnifiedResponse.RawProtocol` / `Chunk.RawProtocol` fields still guard the degenerate case (single-endpoint provider whose adapter mismatches the ingress). The codec uses Raw only when `RawProtocol` matches its own protocol family.

### Audit tables gain `provider_endpoint`

`request_logs`, `usage_records`, `trace_payloads` carry `provider_endpoint VARCHAR` (migration 00026) for per-endpoint cost attribution. `DispatchResult.Endpoint` propagates through telemetry emit.

## Consequences

- **+** Single credential per vendor (no duplication).
- **+** Cross-protocol failover stays passthrough when the alternate provider also has a matching endpoint.
- **+** Breaker isolation per endpoint (openai-endpoint flap doesn't trip the anthropic endpoint).
- **+** Audit/usage attribution to the specific endpoint (not just the provider).
- **−** providers table `adapter` column becomes "primary endpoint's adapter" (still useful for list display; no longer the runtime selector).
- **−** Provider spec JSONB shape change (pre-launch; no migration burden).
- **−** Endpoint.ID must be unique within a provider (adapter-derived default: openai→"openai", claude→"anthropic"; explicitly required when two endpoints share an adapter).

## Relationship to ADR-0047

ADR-0047's core insight — protocol-aware routing enables implicit passthrough — is preserved. The change is granularity: ADR-0047 partitioned at provider level (requiring two provider records for a dual-protocol vendor); ADR-0049 partitions at endpoint level (one provider, multiple endpoints). The `preferProtocol` function is replaced by `expandCandidates`; the `RawProtocol` gating is retained. ADR-0047 is superseded.

## Alternatives considered

- **Keep ADR-0047 (two providers) + add provider groups for credential sharing.** Rejected: introduces a new abstraction layer, more configuration steps (2 providers + 1 group + binding), and doesn't solve cross-protocol failover degradation.
- **Keep ADR-0047 + db:// credential aliasing.** Rejected: solves credential sharing only; doesn't address operational noise or failover degradation.
