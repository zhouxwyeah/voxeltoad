import { handleAdminError } from "@/lib/errors";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { TraceDetailClient } from "../detail-client";
import { fetchDetailByRow } from "../fetch-detail";

export const dynamic = "force-dynamic";

export default async function TraceRawPage({ params }: {
  params: Promise<{ session_id: string; req: string }>;
}) {
  const { session_id: sessionID, req } = await params;
  const rowID = Number(req);
  let current;
  try {
    current = await fetchDetailByRow(rowID);
  } catch (err) {
    const outcome = await handleAdminError(err);
    return <div className="p-8"><ForbiddenNotice message={outcome.message} /></div>;
  }
  return <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
    <TraceDetailClient sessionID={sessionID} requestID={String(rowID)} view="raw" current={current} previous={null} />
  </div>;
}
