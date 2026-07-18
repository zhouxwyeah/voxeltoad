import { serverAdminClient } from "@/lib/admin";
import { onAuthExpired } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { getSession } from "@/lib/session";
import { RequestLogsPageClient } from "./client";

export const dynamic = "force-dynamic";

/**
 * Request logs advanced search (P1). Combines tenant (super-admin only),
 * group_name, api_key_id, provider, model_requested, error_type, blocked_by,
 * stream, fallback, and a time range — all URL-driven so filters are shareable.
 * Offset-paginated: page (1-based) + page_size drive the backend window.
 */
export default async function RequestLogsPage({
  searchParams,
}: {
  searchParams: Promise<{
    page?: string;
    page_size?: string;
    from?: string;
    to?: string;
    tenant?: string;
    group_name?: string;
    api_key_id?: string;
    provider?: string;
    model_requested?: string;
    error_type?: string;
    blocked_by?: string;
    stream?: string;
    fallback?: string;
    session_id?: string;
  }>;
}) {
  const {
    page: pageParam,
    page_size: pageSizeParam,
    from,
    to,
    tenant,
    group_name,
    api_key_id,
    provider,
    model_requested,
    error_type,
    blocked_by,
    stream,
    fallback,
    session_id,
  } = await searchParams;
  const session = await getSession();
  const isSuperAdmin = session.role === "super-admin";

  // Default to the last 30 days when no range is provided.
  const now = new Date();
  const thirtyDaysAgo = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
  const fromValue = from ?? thirtyDaysAgo.toISOString();
  const toValue = to ?? now.toISOString();

  let rows: Array<Record<string, unknown>> = [];
  let total = 0;
  let page = pageParam ? Number(pageParam) : 1;
  let pageSize = pageSizeParam ? Number(pageSizeParam) : 20;
  if (!Number.isFinite(page) || page < 1) page = 1;
  if (!Number.isFinite(pageSize) || pageSize < 1) pageSize = 20;
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number | boolean> = {
      page,
      page_size: pageSize,
    };
    if (fromValue) query.from = fromValue;
    if (toValue) query.to = toValue;
    if (isSuperAdmin && tenant) query.tenant = tenant;
    if (group_name) query.group_name = group_name;
    if (api_key_id) query.api_key_id = api_key_id;
    if (provider) query.provider = provider;
    if (model_requested) query.model_requested = model_requested;
    if (error_type) query.error_type = error_type;
    if (blocked_by) query.blocked_by = blocked_by;
    if (stream) query.stream = stream;
    if (fallback) query.fallback = fallback;
    if (session_id) query.session_id = session_id;
    const res = unwrap(
      await client.GET("/api/v1/request-logs", { params: { query } }),
    ) as Record<string, unknown>;
    rows = (res.data ?? []) as Array<Record<string, unknown>>;
    total = (res as { total?: number }).total ?? 0;
    page = (res as { page?: number }).page ?? page;
    pageSize = (res as { page_size?: number }).page_size ?? pageSize;
  } catch (err) {
    await onAuthExpired(err);
  }

  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-6 p-8">
      <RequestLogsPageClient
        rows={rows}
        total={total}
        page={page}
        pageSize={pageSize}
        isSuperAdmin={isSuperAdmin}
      />
    </div>
  );
}
