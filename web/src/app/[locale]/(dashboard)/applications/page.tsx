import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ApplicationsPageClient } from "./client";

export const dynamic = "force-dynamic";

export default async function ApplicationsPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: Array<Record<string, unknown>> = [];
  let nextCursor = "";
  let groups: Array<Record<string, unknown>> = [];
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    const [appsRes, groupsRes] = await Promise.all([
      client.GET("/api/v1/applications", { params: { query } }),
      client.GET("/api/v1/groups", { params: { query: { limit: 500 } } }),
    ]);
    const page = unwrap(appsRes);
    const groupsPage = unwrap(groupsRes);
    rows = (page.data ?? []) as Array<Record<string, unknown>>;
    nextCursor = page.next_cursor ?? "";
    groups = (groupsPage.data ?? []) as Array<Record<string, unknown>>;
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <ApplicationsPageClient rows={rows} nextCursor={nextCursor} groups={groups} />
    </div>
  );
}
