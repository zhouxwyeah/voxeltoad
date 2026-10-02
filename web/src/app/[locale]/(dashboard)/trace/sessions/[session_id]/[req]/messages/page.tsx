import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { TraceDetailClient } from "../detail-client";
import { fetchDetailByRow } from "../fetch-detail";

export const dynamic = "force-dynamic";

export default async function TraceMessagesPage({ params }: {
  params: Promise<{ session_id: string; req: string }>;
}) {
  const { session_id: sessionID, req } = await params;
  const rowID = Number(req);
  let current;
  let previous = null;
  try {
    current = await fetchDetailByRow(rowID);
    if (current) {
      const client = await serverAdminClient();
      const trace = unwrap(await client.GET("/api/v1/trace/sessions/{session_id}", { params: { path: { session_id: sessionID } } }));
      const rows = trace.requests ?? [];
      const index = rows.findIndex((row) => row.id === rowID);
      const previousID = index > 0 ? rows[index - 1].id : undefined;
      if (previousID) previous = await fetchDetailByRow(previousID);
    }
  } catch (err) {
    const outcome = await handleAdminError(err);
    return <div className="p-8"><ForbiddenNotice message={outcome.message} /></div>;
  }
  return <div className="mx-auto flex max-w-4xl flex-col gap-6 p-8">
    <TraceDetailClient sessionID={sessionID} requestID={String(rowID)} view="messages" current={current} previous={previous} />
  </div>;
}
