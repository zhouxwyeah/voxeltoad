import { serverAdminClient } from "@/lib/admin";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { TraceDetailClient } from "../detail-client";
import { fetchDetailByRow } from "../fetch-detail";

export const dynamic = "force-dynamic";

/**
 * Messages view (direct-URL access): renders the 6-segment category view for a
 * trace_payloads row. The `[req]` URL param is the trace row id (unique).
 */
export default async function TraceMessagesPage({
  params,
}: {
  params: Promise<{ session_id: string; req: string }>;
}) {
  const { session_id: rawSessionId, req: rawReq } = await params;
  const sessionID = decodeURIComponent(rawSessionId);
  const rowID = parseInt(decodeURIComponent(rawReq), 10);

  const current = await fetchDetailByRow(rowID);

  // Resolve the predecessor for the carried-over diff.
  let previous = null;
  try {
    const client = await serverAdminClient();
    const traceRes = unwrap(
      await client.GET("/api/v1/trace/sessions/{session_id}", {
        params: { path: { session_id: sessionID } },
      }),
    ) as Record<string, unknown>;
    const reqs = (traceRes.requests ?? []) as { id?: number }[];
    const idx = reqs.findIndex((r) => r.id === rowID);
    if (idx > 0 && reqs[idx - 1].id) {
      previous = await fetchDetailByRow(reqs[idx - 1].id!);
    }
  } catch {
    previous = null;
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-6 p-8">
      <TraceDetailClient
        sessionID={sessionID}
        requestID={String(rowID)}
        view="messages"
        current={current}
        previous={previous}
      />
    </div>
  );
}
