"use server";

import { AdminError } from "@voxeltoad/gateway-sdk/admin";
import { toFormError } from "@/lib/errors";
import { fetchDetailPair } from "./fetch-detail";
import type { TraceDetail } from "./detail-client";

export async function fetchTraceDetailPair(rowID: number, previousRowID: number): Promise<
  | { ok: true; current: TraceDetail | null; previous: TraceDetail | null }
  | { ok: false; forbidden: boolean }
> {
  try {
    return { ok: true, ...await fetchDetailPair(rowID, previousRowID) };
  } catch (err) {
    await toFormError(err);
    return { ok: false, forbidden: err instanceof AdminError && err.status === 403 };
  }
}
