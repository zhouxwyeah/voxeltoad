import { TraceDetailClient } from "../detail-client";
import { fetchDetailByRow } from "../fetch-detail";

export const dynamic = "force-dynamic";

/**
 * Raw view (direct-URL access): renders the verbatim request/response/error
 * bodies for a trace_payloads row. The `[req]` URL param is the trace row id.
 */
export default async function TraceRawPage({
  params,
}: {
  params: Promise<{ session_id: string; req: string }>;
}) {
  const { session_id: rawSessionId, req: rawReq } = await params;
  const sessionID = decodeURIComponent(rawSessionId);
  const rowID = parseInt(decodeURIComponent(rawReq), 10);

  const current = await fetchDetailByRow(rowID);

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <TraceDetailClient
        sessionID={sessionID}
        requestID={String(rowID)}
        view="raw"
        current={current}
        previous={null}
      />
    </div>
  );
}
