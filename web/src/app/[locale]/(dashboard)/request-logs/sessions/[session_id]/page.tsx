import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { SessionTraceClient } from "./client";

export const dynamic = "force-dynamic";

type RequestEntry = Record<string, unknown>;
type CostSummary = {
  session_id?: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  cost?: number;
  request_count?: number;
};

/**
 * Session trace page: fetches the chronological request timeline + cost summary
 * for a given session_id. Read by all authenticated operators (tenant-scoped
 * at the API level).
 */
export default async function SessionTracePage({
  params,
}: {
  params: Promise<{ session_id: string }>;
}) {
  const { session_id: rawSessionId } = await params;
  const sessionID = decodeURIComponent(rawSessionId);

  let requests: RequestEntry[] = [];
  let costSummary: CostSummary = {};
  let loadError = false;
  try {
    const client = await serverAdminClient();
    const res = unwrap(
      await client.GET("/api/v1/request-logs/sessions/{session_id}", {
        params: { path: { session_id: sessionID } },
      }),
    ) as Record<string, unknown>;
    requests = (res.requests ?? []) as RequestEntry[];
    costSummary = (res.cost_summary ?? {}) as CostSummary;
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <SessionTraceClient
        sessionID={sessionID}
        requests={requests}
        costSummary={costSummary}
        loadError={loadError}
      />
    </div>
  );
}
