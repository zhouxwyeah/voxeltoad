import type {
  APIKeyView,
  ConfigWriteResult,
  Model,
  OffsetEnvelope,
  OverviewResult,
  PlaygroundResult,
  PromptPayload,
  PromptTemplate,
  Provider,
  RequestLogView,
  Route,
  SessionSummary,
  SettingsView,
  TraceDetail,
  TraceSummary,
} from "./types";

// The desktop UI is served by the gateway on the same origin as the read API,
// so relative URLs resolve correctly both when the gateway serves the built
// SPA and during `vite dev` (with the API proxied to the gateway port).
const BASE = "/api/v1";

async function getJSON<T>(path: string, params?: Record<string, string | number | undefined>): Promise<T> {
  const url = new URL(BASE + path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    }
  }
  const resp = await fetch(url.toString());
  if (!resp.ok) {
    let msg = `请求失败 (${resp.status})`;
    try {
      const body = (await resp.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return (await resp.json()) as T;
}

export function getOverview(from?: string, to?: string): Promise<OverviewResult> {
  return getJSON<OverviewResult>("/overview", { from, to });
}

export function listSessions(opts: {
  agent_type?: string;
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
}): Promise<OffsetEnvelope<SessionSummary>> {
  return getJSON<OffsetEnvelope<SessionSummary>>("/sessions", opts);
}

export function listRequestLogs(opts: {
  provider?: string;
  model_requested?: string;
  error_type?: string;
  session_id?: string;
  request_id?: string;
  agent_type?: string;
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
}): Promise<OffsetEnvelope<RequestLogView>> {
  return getJSON<OffsetEnvelope<RequestLogView>>("/request-logs", opts);
}

export function listTraceBySession(
  sessionId: string,
  limit = 200,
): Promise<{ session_id: string; requests: TraceSummary[] }> {
  return getJSON<{ session_id: string; requests: TraceSummary[] }>(
    `/trace/sessions/${encodeURIComponent(sessionId)}`,
    { limit },
  );
}

export function getTraceByRowID(id: number): Promise<TraceDetail> {
  return getJSON<TraceDetail>(`/trace/rows/${id}`);
}

export function getTraceByRequestID(requestId: string): Promise<TraceDetail> {
  return getJSON<TraceDetail>(`/trace/requests/${encodeURIComponent(requestId)}`);
}

export function getLogs(tail = 500): Promise<{ lines: string[] }> {
  return getJSON<{ lines: string[] }>("/logs", { tail });
}

// --- config CRUD (providers / models / routes) + reload ---

async function sendJSON<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: { "Content-Type": "application/json" } };
  if (body !== undefined) init.body = JSON.stringify(body);
  const resp = await fetch(BASE + path, init);
  const text = await resp.text();
  let parsed: unknown = null;
  if (text) {
    try { parsed = JSON.parse(text); } catch { /* non-JSON body, fall through */ }
  }
  if (!resp.ok) {
    const errMsg = (parsed as { error?: string } | null)?.error ?? `请求失败 (${resp.status})`;
    throw new Error(errMsg);
  }
  return parsed as T;
}

export function listProviders(): Promise<Provider[]> {
  return getJSON<Provider[]>("/providers");
}
export function createProvider(p: Provider): Promise<ConfigWriteResult<Provider>> {
  return sendJSON<ConfigWriteResult<Provider>>("POST", "/providers", p);
}
export function updateProvider(name: string, p: Provider): Promise<ConfigWriteResult<Provider>> {
  return sendJSON<ConfigWriteResult<Provider>>("PUT", `/providers/${encodeURIComponent(name)}`, p);
}
export function deleteProvider(name: string): Promise<ConfigWriteResult> {
  return sendJSON<ConfigWriteResult>("DELETE", `/providers/${encodeURIComponent(name)}`);
}

export function listModels(): Promise<Model[]> {
  return getJSON<Model[]>("/models");
}
export function createModel(m: Model): Promise<ConfigWriteResult<Model>> {
  return sendJSON<ConfigWriteResult<Model>>("POST", "/models", m);
}
export function updateModel(alias: string, m: Model): Promise<ConfigWriteResult<Model>> {
  return sendJSON<ConfigWriteResult<Model>>("PUT", `/models/${encodeURIComponent(alias)}`, m);
}
export function deleteModel(alias: string): Promise<ConfigWriteResult> {
  return sendJSON<ConfigWriteResult>("DELETE", `/models/${encodeURIComponent(alias)}`);
}

export function listRoutes(): Promise<Route[]> {
  return getJSON<Route[]>("/routes");
}
export function createRoute(r: Route): Promise<ConfigWriteResult<Route>> {
  return sendJSON<ConfigWriteResult<Route>>("POST", "/routes", r);
}
export function updateRoute(alias: string, r: Route): Promise<ConfigWriteResult<Route>> {
  return sendJSON<ConfigWriteResult<Route>>("PUT", `/routes/${encodeURIComponent(alias)}`, r);
}
export function deleteRoute(alias: string): Promise<ConfigWriteResult> {
  return sendJSON<ConfigWriteResult>("DELETE", `/routes/${encodeURIComponent(alias)}`);
}

export function reloadConfig(): Promise<ConfigWriteResult<{ status: string }>> {
  return sendJSON<ConfigWriteResult<{ status: string }>>("POST", "/config/reload");
}

export function getSettings(): Promise<SettingsView> {
  return getJSON<SettingsView>("/settings");
}
export function updateSettings(s: SettingsView): Promise<ConfigWriteResult<SettingsView>> {
  return sendJSON<ConfigWriteResult<SettingsView>>("PUT", "/settings", s);
}

export function getAPIKey(): Promise<APIKeyView> {
  return getJSON<APIKeyView>("/apikey");
}
export function rotateAPIKey(): Promise<APIKeyView & { warning?: string }> {
  return sendJSON<APIKeyView & { warning?: string }>("POST", "/apikey/rotate");
}

export function playgroundChat(model: string, prompt: string): Promise<PlaygroundResult> {
  return sendJSON<PlaygroundResult>("POST", "/playground/chat", { model, prompt });
}

// --- prompt favorites ---

export function listPrompts(opts: {
  q?: string;
  tag?: string;
  page?: number;
  page_size?: number;
}): Promise<OffsetEnvelope<PromptTemplate>> {
  return getJSON<OffsetEnvelope<PromptTemplate>>("/prompts", opts);
}
export function createPrompt(p: PromptPayload): Promise<ConfigWriteResult<PromptTemplate>> {
  return sendJSON<ConfigWriteResult<PromptTemplate>>("POST", "/prompts", p);
}
export function updatePrompt(id: number, p: PromptPayload): Promise<ConfigWriteResult<PromptTemplate>> {
  return sendJSON<ConfigWriteResult<PromptTemplate>>("PUT", `/prompts/${id}`, p);
}
export function deletePrompt(id: number): Promise<ConfigWriteResult> {
  return sendJSON<ConfigWriteResult>("DELETE", `/prompts/${id}`);
}
