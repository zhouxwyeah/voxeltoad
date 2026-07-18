import { serverAdminClient } from "@/lib/admin";
import { onAuthExpired } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { AuditPageClient } from "./client";

export const dynamic = "force-dynamic";

/**
 * Audit log list RSC. Fetches management-plane mutation audit entries
 * server-side; tenant-admin is scoped to its own tenant by the admin API.
 * Offset-paginated: page (1-based) + page_size drive the backend window.
 */
export default async function AuditPage({
  searchParams,
}: {
  searchParams: Promise<{
    page?: string;
    page_size?: string;
    from?: string;
    to?: string;
    resource_type?: string;
    action?: string;
  }>;
}) {
  const {
    page: pageParam,
    page_size: pageSizeParam,
    from,
    to,
    resource_type,
    action,
  } = await searchParams;

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
    const query: Record<string, string | number> = {
      page,
      page_size: pageSize,
    };
    if (fromValue) query.from = fromValue;
    if (toValue) query.to = toValue;
    if (resource_type) query.resource_type = resource_type;
    if (action) query.action = action;

    const res = unwrap(await client.GET("/api/v1/audit", { params: { query } }));
    rows = (res.data ?? []) as Array<Record<string, unknown>>;
    total = res.total ?? 0;
    page = res.page ?? page;
    pageSize = res.page_size ?? pageSize;
  } catch (err) {
    await onAuthExpired(err);
  }

  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-6 p-8">
      <AuditPageClient rows={rows} total={total} page={page} pageSize={pageSize} />
    </div>
  );
}
