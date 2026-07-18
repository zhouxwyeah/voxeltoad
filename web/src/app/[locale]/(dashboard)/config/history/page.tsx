import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ConfigHistoryPageClient } from "./client";

export const dynamic = "force-dynamic";

/**
 * Config snapshot history list (ADR-0025). Super-admin only — the nav link is
 * gated by role and the admin API returns 403 for tenant-admins. Each row is a
 * {version, created_at} summary; the full snapshot is loaded on demand when
 * the user opens the diff/rollback dialog.
 */
export default async function ConfigHistoryPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: Array<{ version: number; created_at?: string }> = [];
  let nextCursor = "";

  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    const page = unwrap(
      await client.GET("/api/v1/config/history", { params: { query } }),
    );
    rows = (page.data ?? []) as Array<{ version: number; created_at?: string }>;
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
      <ConfigHistoryPageClient rows={rows} nextCursor={nextCursor} />
    </div>
  );
}
