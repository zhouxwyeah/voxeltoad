import "server-only";

import { serverAdminClient } from "@/lib/admin";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import type { TraceDetail } from "./detail-client";

/**
 * fetchDetailByRow fetches the full trace payload for a single trace_payloads
 * row by its identity primary key. The row id is unique, so this resolves each
 * request to its own payload — unlike the request_id lookup, which uses LIMIT 1
 * and thus returns the same row when a client sends duplicate X-Request-Id
 * headers across requests in a session.
 *
 * Used by two callers:
 *  - RSC sub-routes ([req]/messages, [req]/raw): render the detail server-side.
 *  - The fetchTraceDetailPair Server Action: powers the in-place right-panel
 *    update (no navigation) in the session detail client.
 */
export async function fetchDetailByRow(rowID: number): Promise<TraceDetail | null> {
  if (!rowID || rowID <= 0) return null;
  try {
    const client = await serverAdminClient();
    const res = unwrap(
      await client.GET("/api/v1/trace/rows/{id}", {
        params: { path: { id: rowID } },
      }),
    );
    return (res ?? null) as TraceDetail | null;
  } catch {
    return null;
  }
}

/**
 * fetchDetailPair fetches the current request's detail plus the previous
 * request's detail (both by row id) for the carried-over message diff.
 * previousRowID is 0 for the first request in a session (no predecessor).
 */
export async function fetchDetailPair(
  rowID: number,
  previousRowID: number,
): Promise<{ current: TraceDetail | null; previous: TraceDetail | null }> {
  const [current, previous] = await Promise.all([
    fetchDetailByRow(rowID),
    previousRowID > 0 ? fetchDetailByRow(previousRowID) : Promise.resolve(null),
  ]);
  return { current, previous };
}
