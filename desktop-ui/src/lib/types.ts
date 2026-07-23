// DTOs matching the Go JSON output of cmd/desktop/store (configurable field
// names, see store/query.go).

export interface RequestLogView {
  id: number;
  tenant: string;
  group_name: string;
  api_key_id: string;
  provider: string;
  model_requested: string;
  model_resolved: string;
  stream: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  ttft_ms: number;
  duration_ms: number;
  error_type: string;
  blocked_by: string;
  fallback: boolean;
  request_id: string;
  client_request_id: string;
  session_id: string;
  trace_id: string;
  session_source: string;
  agent_type: string;
  cache_hit: boolean;
  cache_tier: string;
  cache_source: string;
  cached_prompt_tokens: number;
  upstream_request_id: string;
  created_at: string;
}

export interface SessionSummary {
  session_id: string;
  agent_type: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  duration_ms: number;
  started_at: string;
  last_seen: string;
  has_errors: boolean;
}

export interface TraceSummary {
  id: number;
  request_id: string;
  client_request_id: string;
  session_id: string;
  trace_id: string;
  tenant: string;
  provider: string;
  model_requested: string;
  stream: boolean;
  agent_type: string;
  status_code: number;
  stop_reason: string;
  n_messages: number;
  n_tool_use: number;
  created_at: string;
}

// messages / request_raw arrive as already-parsed JSON (json.RawMessage emitted
// verbatim), so they are typed as unknown and handed to TraceCategories.
export interface TraceDetail extends TraceSummary {
  messages: unknown;
  request_raw: unknown;
  response_raw: string;
  error_raw: string;
}

export interface AgentUsage {
  agent_type: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  duration_ms: number;
  ttft_ms: number;
  error_count: number;
}

export interface OffsetEnvelope<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface OverviewResult {
  agents: AgentUsage[];
  totals: AgentUsage;
}

// --- Config types (match internal/config/schema.go JSON shape) ---
// time.Duration fields arrive as nanoseconds (int64); 1s = 1_000_000_000.

export interface ProviderTimeouts {
  connect: number;
  first_byte: number;
  overall: number;
}

export interface Provider {
  name: string;
  type: string;
  adapter: string;
  base_url: string;
  api_key_ref: string;
  timeouts: ProviderTimeouts;
  weight: number;
}

export interface Pricing {
  prompt_per_1m: number;
  completion_per_1m: number;
  currency: string;
  cache_hit_multiplier?: number;
}

export interface ModelUpstream {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing: Pricing;
}

export interface Model {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams: ModelUpstream[];
}

export interface RouteProvider {
  name: string;
  weight?: number;
}

export interface Route {
  model_alias: string;
  providers: RouteProvider[];
  strategy: string;
}

// Envelope returned by config write endpoints (POST/PUT/DELETE).
export interface ConfigWriteResult<T = unknown> {
  data: T;
  warning?: string;
}

// /api/v1/settings payload: editable gateway settings (design/desktop.md §7).
// gateway.* is bootstrap-only (restart-applied); trace.* hot-applies on save.
export interface GatewaySettingsView {
  addr: string;
  session_headers: string[];
}

export interface TraceSettingsView {
  capture_payload_enabled: boolean;
  max_body_kb: number;
  retention_days: number;
}

export interface SettingsView {
  gateway: GatewaySettingsView;
  trace: TraceSettingsView;
}

// /api/v1/apikey payload. `key` is present only while the plaintext is known
// (seeded/env at startup, or just rotated); after a restart with a rotated
// key the plaintext is unrecoverable and only `plaintext_known: false` shows.
export interface APIKeyView {
  key_id: string;
  key?: string;
  plaintext_known: boolean;
}

// /api/v1/playground/chat result. On upstream failure the endpoint answers
// 502 with {error, provider, latency_ms} — surfaced verbatim by the page.
// Thinking models may return empty content with reasoning_content set (the
// chain-of-thought that consumed the output budget).
export interface PlaygroundResult {
  content: string;
  provider: string;
  model_resolved: string;
  fallback: boolean;
  latency_ms: number;
  finish_reason?: string;
  reasoning_content?: string;
  usage?: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
}

// /api/v1/prompts — favorited prompt templates (design/desktop.md §10.3-7).
export interface PromptTemplate {
  id: number;
  title: string;
  content: string;
  tags: string[];
  session_id?: string;
  source_trace_row_id?: number;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface PromptPayload {
  title: string;
  content: string;
  tags: string[];
  note: string;
  session_id?: string;
  source_trace_row_id?: number;
}
