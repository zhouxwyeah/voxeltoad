import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { TraceSessionsClient } from "./client";

export const dynamic = "force-dynamic";

export type SessionSummary = {
  session_id: string;
  agent_type: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  duration_ms: number;
  cost: number;
  started_at: string;
  last_seen: string;
  has_errors: boolean;
};

/**
 * Trace landing page: lists sessions aggregated server-side from request_logs
 * + usage_records via GET /api/v1/request-logs/sessions (per-session token,
 * duration, cost, and agent totals). Each session links to its trace detail.
 * Tenant scope is enforced at the API level.
 */
export default async function TracePage({
  searchParams,
}: {
  searchParams: Promise<{ agent_type?: string; from?: string; to?: string }>;
}) {
  const sp = await searchParams;
  let sessions: SessionSummary[] = [];
  let anyRows = false;

  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = { page: 1, page_size: 100 };
    if (sp.agent_type) query.agent_type = sp.agent_type;
    if (sp.from) query.from = sp.from;
    if (sp.to) query.to = sp.to;
    const res = unwrap(
      await client.GET("/api/v1/request-logs/sessions", {
        params: { query },
      }),
    );
    sessions = (res.data ?? []) as SessionSummary[];
    anyRows = sessions.length > 0;
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
      <TraceSessionsClient
        sessions={sessions}
        anyRows={anyRows}
        agentFilter={sp.agent_type ?? ""}
      />
    </div>
  );
}
