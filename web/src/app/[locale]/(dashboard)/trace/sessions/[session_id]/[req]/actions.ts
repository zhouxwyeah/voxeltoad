"use server";

import { fetchDetailPair } from "./fetch-detail";
import type { TraceDetail } from "./detail-client";

/**
 * fetchTraceDetailPair is the Server Action powering the in-place right-panel
 * update. It fetches the selected trace_payloads row and its chronological
 * predecessor BY ROW ID (the table's unique primary key), not by request_id —
 * some clients send duplicate X-Request-Id headers, making request_id
 * non-unique within a session.
 *
 * Returns { current, previous } so the client can derive the carried-over
 * message category (longest-common-prefix diff).
 */
export async function fetchTraceDetailPair(
  rowID: number,
  previousRowID: number,
): Promise<{ current: TraceDetail | null; previous: TraceDetail | null }> {
  return fetchDetailPair(rowID, previousRowID);
}
