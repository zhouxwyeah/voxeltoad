import { serverAdminClient } from "@/lib/admin";
import { onAuthExpired } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { getSession } from "@/lib/session";
import { UsagePageClient } from "./client";

export const dynamic = "force-dynamic";

/**
 * Usage list + summary RSC (P1: drill-down). The summary group_by dimension is
 * URL-driven (default tenant for super-admin, model for tenant-admin) so the
 * operator can pivot the aggregate view without a round-trip.
 */
export default async function UsagePage({
  searchParams,
}: {
  searchParams: Promise<{
    cursor?: string;
    limit?: string;
    from?: string;
    to?: string;
    tenant?: string;
    provider?: string;
    model?: string;
    group_by?: string;
    bucket?: string;
  }>;
}) {
  const {
    cursor,
    limit,
    from,
    to,
    tenant,
    provider,
    model,
    group_by,
    bucket,
  } = await searchParams;
  const session = await getSession();
  const isSuperAdmin = session.role === "super-admin";

  // Default to the last 30 days when no range is provided.
  const now = new Date();
  const thirtyDaysAgo = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
  const fromValue = from ?? thirtyDaysAgo.toISOString();
  const toValue = to ?? now.toISOString();

  let rows: Array<Record<string, unknown>> = [];
  let nextCursor = "";
  let summaryRows: Array<Record<string, unknown>> = [];
  let tenants: Array<Record<string, unknown>> = [];
  let timeseriesRows: Array<Record<string, unknown>> = [];

  try {
    const client = await serverAdminClient();

    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    if (fromValue) query.from = fromValue;
    if (toValue) query.to = toValue;
    if (isSuperAdmin && tenant) query.tenant = tenant;
    if (provider) query.provider = provider;
    if (model) query.model = model;

    const page = unwrap(
      await client.GET("/api/v1/usage", { params: { query } }),
    ) as Record<string, unknown>;
    rows = (page.data ?? []) as Array<Record<string, unknown>>;
    nextCursor = (page as { next_cursor?: string }).next_cursor ?? "";

    // group_by URL param controls the summary dimension. Tenant-admin always
    // groups by model (the API ignores other dimensions for non-super-admin).
    const validDimensions = ["tenant", "group_name", "api_key_id", "provider", "model"];
    const requestedDim = group_by ?? (isSuperAdmin ? "tenant" : "model");
    const summaryGroupBy = validDimensions.includes(requestedDim)
      ? requestedDim
      : "model";
    const summaryQuery: Record<string, string | number> = {
      group_by: summaryGroupBy,
    };
    if (fromValue) summaryQuery.from = fromValue;
    if (toValue) summaryQuery.to = toValue;
    if (isSuperAdmin && tenant) summaryQuery.tenant = tenant;

    const summaryPage = unwrap(
      await client.GET("/api/v1/usage/summary", { params: { query: summaryQuery } }),
    );
    summaryRows = (summaryPage.data ?? []) as Array<Record<string, unknown>>;

    // Timeseries for the trend chart. bucket defaults to "day".
    const validBuckets = ["hour", "day", "week"];
    const bucketParam = bucket && validBuckets.includes(bucket) ? bucket : "day";
    const tsQuery: Record<string, string | number> = { bucket: bucketParam };
    if (fromValue) tsQuery.from = fromValue;
    if (toValue) tsQuery.to = toValue;
    if (isSuperAdmin && tenant) tsQuery.tenant = tenant;
    if (provider) tsQuery.provider = provider;
    if (model) tsQuery.model = model;
    const tsPage = unwrap(
      await client.GET("/api/v1/usage/timeseries", { params: { query: tsQuery } }),
    );
    timeseriesRows = (tsPage.data ?? []) as Array<Record<string, unknown>>;

    if (isSuperAdmin) {
      const tenantsPage = unwrap(await client.GET("/api/v1/tenants"));
      tenants = (tenantsPage.data ?? []) as Array<Record<string, unknown>>;
    }
  } catch (err) {
    await onAuthExpired(err);
  }

  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-6 p-8">
      <UsagePageClient
        rows={rows}
        nextCursor={nextCursor}
        summaryRows={summaryRows}
        tenants={tenants}
        timeseriesRows={timeseriesRows}
        isSuperAdmin={isSuperAdmin}
        groupBy={group_by ?? ""}
      />
    </div>
  );
}
