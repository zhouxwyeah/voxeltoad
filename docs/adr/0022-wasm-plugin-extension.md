# ADR-0022: WASM plugin extension point (wazero)

- Status: Proposed
- Date: 2026-07-04
- Supersedes: 2026-06-29 design plan §3.4 (WASM P1 placeholder → concrete ABI)

## Context

The plugin framework (`internal/plugin/plugin.go`) has a mature Go-native interface:
built-in governance plugins (ratelimit, billing, cache, sensitive, pii) are
compiled into the gateway binary. The 2026-06-29 design plan §3.4 reserves WASM
(`wazero`) as the P1 extension point for user-defined plugins so operators can
add governance logic without rebuilding the gateway.

The built-in plugin surface (Phase 2 D1 + Phase 3 D2) now covers:
- Content filtering (sensitive keywords, PII redaction)
- Rate limiting (RPM/TPM)
- Quota billing
- Response caching

User-defined WASM plugins are a platform differentiation goal, not a current
governance gap. The complexity lies in the **ABI contract**, not in running WASM
(wazero is mature at v1.15+).

## Decision

### ABI v1 contract

Each WASM module exposes exactly one exported function:

```wasm
// Input:  ptr (int32) → i32 pointer to serialized request JSON in linear memory
//         len (int32) → byte length of the payload
// Output: i64        → encoded as (result_ptr << 32) | result_len,
//          where result_ptr is the i32 pointer and result_len is the byte
//          length of the response JSON in guest linear memory.
fn execute(ptr: i32, len: i32) -> i64
```

**Input payload** (host → guest):

```json
{
  "model": "gpt-4o",
  "stream": true,
  "messages": [
    {"role": "system", "content": "You are helpful."},
    {"role": "user", "content": "Hello."}
  ],
  "tenant": "acme",
  "config": { "custom_key": "value" },
  "phase": "pre"
}
```

**Output payload** (guest → host):

```json
{
  "stop": false,
  "blocked_by": "",
  "rewritten_messages": null,
  "error": ""
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `stop` | bool | yes | Short-circuit the request chain |
| `blocked_by` | string | no | Plugin name for `llm.plugin.blocked_by` |
| `rewritten_messages` | array | no | Replaces `c.Request.Messages` when non-null |
| `error` | string | no | Non-empty → host returns 500, logs error |

### Host functions exposed to guest

A minimal set to keep the sandbox lean:

| Function | Signature | Purpose |
|----------|-----------|---------|
| `log` | `(ptr i32, len i32)` | Write a debug line to host logs |
| `now_ms` | `() i64` | Return current Unix ms for time-based logic |
| `get_config` | `(key_ptr i32, key_len i32, val_ptr i32, val_max i32) i32` | Read a config value into a guest-allocated buffer at `val_ptr` (max `val_max` bytes). Returns bytes written (0 = key not found). Guest owns `val_ptr` allocation — it must reside in linear memory outside the input payload region. |

### Sandbox constraints

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Execution timeout | 10 ms per `execute()` call | Prevent plugin stalls from blocking the request path |
| Linear memory | 128 KB (17 pages) | Sufficient for typical LLM messages + JSON overhead |
| Max concurrent instances | 1 per request (created and destroyed each call) | Isolated linear memory per request; no cross-request state leakage |
| Imported functions | Only the 3 listed | No `wasi_snapshot_preview1` (no FS/networking access) |
| Custom sections | Reject on `start` / `init` function | Only `execute` export is allowed |

### Lifecycle

```
compile: mod, err := wazero.CompileModule(ctx, runtime, cfg.WasmBytes)  ← once at plugin construction
call:    inst, err := runtime.Instantiate(ctx, mod)           ← new instance per request
         fn := inst.ExportedFunction("execute")
         results, err := fn.Call(ctx, ptr, len)
         → decode (result_ptr << 32) | result_len from results[0]
         → read result JSON from inst.Memory
         inst.Close(ctx)                                      ← release per-request instance
```

- `CompileModule` once at plugin construction (reusable across requests).
- `Instantiate` per request — ~1μs overhead, no concurrency risk on linear
  memory.
- `inst.Close()` releases the instance after each request (no leak).

### Integration with plugin.Factory

```go
// internal/plugin/wasmhost/host.go

func NewWASMPlugin(cfg WASMConfig) (*Plugin, error) {
    mod, err := wazero.CompileModule(ctx, runtime, cfg.WasmBytes)
    // ...
    return &Plugin{module: mod, config: cfg}, nil
}
```

The `WASMPlugin` implements `plugin.Plugin`:
- `Name()` returns `cfg.Name` (e.g., "wasm-guardrail-v1")
- `Phases()` returns `[cfg.Phase]` (Pre or Post, configurable)
- `Execute()` serializes `c.Request` + `c.Tenant` to JSON, calls the WASM
  function, and parses the result.

This requires a **new field** on `plugin.Config`: `WASMPluginConfig`—but the
`PluginConfig` Params map already carries arbitrary JSON, so `wasm_bytes`
(base64-encoded) and `wasm_name` can live in `Params` without schema changes.

### Versioning

ABI v1 is intentionally minimal. ABI evolution follows semver:
- MAJOR: breaking input/output schema change → requires plugin recompilation
- MINOR: new optional fields in input/output JSON → backward compatible
- The host sets `"abi_version": 1` in every input payload so guests can branch.

## Consequences

### Positive

- Operators can add custom governance without rebuilding the gateway.
- ABI v1 is lean — 1 function, 3 host exports, no WASI — keeping the sandbox
  tight.
- Reuses the existing plugin.Factory + Register + PluginConfig infrastructure;
  no new management-plane endpoints needed (just upload base64 wasm_bytes as
  PluginConfig.Params).
- wazero's pure-Go runtime eliminates CGo/cross-compilation burden.

### Negative

- Guest must be **stateless** per call — no retained state between requests.
  This is acceptable for Pre/Post governance (inspect + decide) but limits
  stateful plugins (e.g., a custom rate limiter). If stateful plugins are needed
  later, the ABI can add `get_state`/`set_state` host functions.
- Input/output is JSON, not binary — adds serialization overhead. Acceptable
  because LLM request latency (seconds) dominates plugin overhead (microseconds).
- `wasm_bytes` uploaded via admin API must be audited separately (size limits,
  checksum verification, content scanning). The admin CRUD path for plugins
  already exists and can be reused with a size cap added.

### Mitigations

- Guest execution is bounded by 10ms timeout — wazero's `context.WithTimeout`
  covers this natively.
- Memory access is constrained to 128 KB — wazero's `WithMemoryLimitPages`
  enforces this.
- No FS or network imports → guest cannot exfiltrate data or make outbound calls.

## Related

- ADR-0006: API Key caching (trust boundary pattern)
- ADR-0021: Request logs / privacy constraints (WASM guests inherit the "never
  log prompt content" rule)
- design/architecture.md: Plugin framework (L2 plugin layer)
- design/observability.md: Semantic telemetry fields
