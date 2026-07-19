import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { OverviewPageClient } from "./client";
import { rangeToFromTo, type OverviewRange } from "./range";

export const dynamic = "force-dynamic";

// searchParams drives the time-range preset (?range=today|yesterday|week|month|last_month).
// Default is "today". The preset is resolved to UTC RFC3339 from/to and forwarded
// to the backend; the backend stays timezone-agnostic.
export default async function OverviewPage({
  searchParams,
}: {
  searchParams: Promise<{ range?: string }>;
}) {
  const { range: rawRange } = await searchParams;
  const range: OverviewRange = (["today", "yesterday", "week", "month", "last_month"].includes(
    rawRange ?? "",
  )
    ? (rawRange as OverviewRange)
    : "today");
  const { from, to } = rangeToFromTo(range);

  let data: Record<string, unknown> = {};
  try {
    const client = await serverAdminClient();
    const query: Record<string, string> = {};
    if (from) query.from = from;
    if (to) query.to = to;
    const page = unwrap(
      await client.GET("/api/v1/overview", {
        params: { query },
      }),
    );
    data = (page ?? {}) as Record<string, unknown>;
  } catch (err) {
    // 401 redirects to /logout; a 403 (super-admin-only endpoint reached by a
    // tenant-admin via direct URL) renders a no-permission notice instead of
    // crashing the render; anything else re-throws.
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <OverviewPageClient data={data} range={range} />
    </div>
  );
}
