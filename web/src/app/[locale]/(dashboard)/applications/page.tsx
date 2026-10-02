import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { AdminError, unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { getSession } from "@/lib/session";
import { has } from "@/lib/permissions";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ApplicationsPageClient } from "./client";

export const dynamic = "force-dynamic";

export default async function ApplicationsPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;
  const session = await getSession();
  const canReadAttribution = session.scopeKind === "tenant" && has(session, "usage.read");
  const to = new Date();
  const from = new Date(to.getTime() - 24 * 60 * 60 * 1000);
  let attribution: AdminPaths["/api/v1/usage/attribution"]["get"]["responses"][200]["content"]["application/json"] | null = null;
  let attributionError: "error" | "forbidden" | null = null;

  let rows: Array<Record<string, unknown>> = [];
  let nextCursor = "";
  let groups: Array<Record<string, unknown>> = [];
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    const [appsRes, groupsRes, attributionRes] = await Promise.all([
      client.GET("/api/v1/applications", { params: { query } }),
      client.GET("/api/v1/groups", { params: { query: { limit: 500 } } }),
      canReadAttribution
        ? client.GET("/api/v1/usage/attribution", {
            params: { query: { from: from.toISOString(), to: to.toISOString() } },
          }).then(unwrap).catch((err: unknown) => {
            if (err instanceof AdminError && err.status === 401) throw err;
            attributionError = err instanceof AdminError && err.status === 403 ? "forbidden" : "error";
            return null;
          })
        : Promise.resolve(null),
    ]);
    attribution = attributionRes;
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
      <ApplicationsPageClient
        rows={rows}
        nextCursor={nextCursor}
        groups={groups}
        attribution={attribution}
        attributionError={attributionError}
      />
    </div>
  );
}
