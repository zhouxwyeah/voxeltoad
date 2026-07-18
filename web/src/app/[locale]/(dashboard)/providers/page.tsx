import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ProvidersPageClient } from "./client";

// Per-request: reads the session cookie + calls the admin API. Never prerender.
export const dynamic = "force-dynamic";

/**
 * Providers list RSC (design-system.md §9 template A). Fetches data server-side,
 * then hands rows to the client shell which owns Modal state + table interaction.
 */
export default async function ProvidersPage({
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
      await client.GET("/api/v1/providers", { params: { query } }),
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
      <ProvidersPageClient rows={rows} nextCursor={nextCursor} />
    </div>
  );
}
