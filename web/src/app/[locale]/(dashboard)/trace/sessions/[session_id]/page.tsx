import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { SessionDetailClient } from "./client";

export const dynamic = "force-dynamic";

/**
 * TraceRow is one row from trace_payloads — the primary data source for the
 * request list. It carries the unique row `id` (used to fetch each request's
 * detail individually), plus summary dimensions (status, agent, token counts
 * from request_logs are NOT here — they come from the metadata merge).
 */
export type TraceRow = {
  id?: number;          // trace_payloads row id (unique — the fetch key)
  request_id?: string;
  agent_type?: string;
  status_code?: number;
  stop_reason?: string;
  n_messages?: number;
  n_tool_use?: number;
  created_at?: string;
};

/**
 * MetaRow is one row from request_logs — carries token/duration data not in
 * trace_payloads. Merged by chronological position (index) since request_id may
 * be duplicated.
 */
export type MetaRow = {
  request_id?: string;
  status_code?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  duration_ms?: number;
  model_requested?: string;
  created_at?: string;
};

export type SessionStats = {
  agent_type: string;
  started_at: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
};

/**
 * Session trace detail page. The request list is built from trace_payloads rows
 * (each has a unique `id`), with token/duration metadata merged from
 * request_logs by chronological position. The client uses the trace row `id` to
 * fetch each request's detail via the Server Action.
 */
export default async function SessionDetailPage({
  params,
}: {
  params: Promise<{ session_id: string }>;
}) {
  const { session_id: rawSessionId } = await params;
  const sessionID = decodeURIComponent(rawSessionId);

  let traceRows: TraceRow[] = [];
  let metaRows: MetaRow[] = [];
  let cost: number | undefined;

  try {
    const client = await serverAdminClient();

    // Metadata + cost from request_logs + usage_records.
    try {
      const metaRes = unwrap(
        await client.GET("/api/v1/request-logs/sessions/{session_id}", {
          params: { path: { session_id: sessionID } },
        }),
      ) as Record<string, unknown>;
      metaRows = (metaRes.requests ?? []) as MetaRow[];
      const cs = metaRes.cost_summary as { cost?: number } | undefined;
      cost = cs?.cost;
    } catch {
      metaRows = [];
    }

    // Trace summaries from trace_payloads — these carry the unique row id.
    const traceRes = unwrap(
      await client.GET("/api/v1/trace/sessions/{session_id}", {
        params: { path: { session_id: sessionID } },
      }),
    ) as Record<string, unknown>;
    traceRows = (traceRes.requests ?? []) as TraceRow[];
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  // Derive session-level stats.
  const stats: SessionStats = {
    agent_type: traceRows.find((r) => r.agent_type)?.agent_type ?? "",
    started_at: traceRows[0]?.created_at ?? "",
    request_count: traceRows.length,
    prompt_tokens: metaRows.reduce((s, r) => s + (r.prompt_tokens ?? 0), 0),
    completion_tokens: metaRows.reduce((s, r) => s + (r.completion_tokens ?? 0), 0),
    total_tokens: metaRows.reduce((s, r) => s + (r.total_tokens ?? 0), 0),
  };

  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-4 p-6">
      <SessionDetailClient
        sessionID={sessionID}
        traceRows={traceRows}
        metaRows={metaRows}
        stats={stats}
        cost={cost}
      />
    </div>
  );
}
