import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { GroupsPageClient } from "./client";

export const dynamic = "force-dynamic";

export default async function GroupsPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: Array<Record<string, unknown>> = [];
  let nextCursor = "";
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    const page = unwrap(
      await client.GET("/api/v1/groups", { params: { query } }),
    );
    rows = (page.data ?? []) as Array<Record<string, unknown>>;
    nextCursor = page.next_cursor ?? "";
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
      <GroupsPageClient rows={rows} nextCursor={nextCursor} />
    </div>
  );
}
