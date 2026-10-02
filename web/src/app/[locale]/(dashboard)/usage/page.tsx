import type { ComponentProps } from "react";
import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { getSession } from "@/lib/session";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { UsagePageClient } from "./client";

export const dynamic = "force-dynamic";

export default async function UsagePage({ searchParams }: {
  searchParams: Promise<{
    cursor?: string; limit?: string; from?: string; to?: string; tenant?: string;
    provider?: string; model?: string; group_by?: string; bucket?: string;
    application_id?: string; environment?: string; unattributed?: string;
  }>;
}) {
  const params = await searchParams;
  const session = await getSession();
  const isSuperAdmin = session.role === "super-admin";
  const now = new Date();
  const from = params.from ?? new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000).toISOString();
  const to = params.to ?? now.toISOString();
  const validDimensions = ["tenant", "group_name", "api_key_id", "provider", "model", "application_id", "environment", "currency"] as const;
  const requestedDim = params.group_by ?? (isSuperAdmin ? "tenant" : "model");
  const groupBy = validDimensions.find((dimension) => dimension === requestedDim) ?? "model";
  const bucket = (["hour", "day", "week"] as const).find((value) => value === params.bucket) ?? "day";
  let props: ComponentProps<typeof UsagePageClient>;
  try {
    const client = await serverAdminClient();
    const filters = {
      from, to,
      tenant: isSuperAdmin ? params.tenant : undefined,
      provider: params.provider,
      model: params.model,
      application_id: params.application_id ? Number(params.application_id) : undefined,
      environment: params.environment as NonNullable<AdminPaths["/api/v1/usage"]["get"]["parameters"]["query"]>["environment"],
      unattributed: params.unattributed === "true" || undefined,
    };
    const [listResult, summaryResult, timeseriesResult] = await Promise.all([
      client.GET("/api/v1/usage", { params: { query: { ...filters, cursor: params.cursor, limit: params.limit ? Number(params.limit) : undefined } } }),
      client.GET("/api/v1/usage/summary", { params: { query: { ...filters, group_by: groupBy } } }),
      client.GET("/api/v1/usage/timeseries", { params: { query: { ...filters, bucket } } }),
    ]);
    const page = unwrap(listResult);
    if (typeof page === "string") throw new Error("Expected JSON usage response");
    const summary = unwrap(summaryResult);
    const timeseries = unwrap(timeseriesResult);
    const tenants = isSuperAdmin ? unwrap(await client.GET("/api/v1/tenants")).data ?? [] : [];
    props = { rows: page.data ?? [], nextCursor: page.next_cursor ?? "", summaryRows: summary.data ?? [], tenants, timeseriesRows: timeseries.data ?? [], isSuperAdmin, groupBy };
  } catch (err) {
    const outcome = await handleAdminError(err);
    return <div className="p-8"><ForbiddenNotice message={outcome.message} /></div>;
  }
  return <div className="mx-auto flex max-w-7xl flex-col gap-6 p-8"><UsagePageClient key={JSON.stringify(params)} {...props} /></div>;
}
