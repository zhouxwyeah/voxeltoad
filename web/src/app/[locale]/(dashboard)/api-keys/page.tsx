import type { ComponentProps } from "react";
import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { APIKeysPageClient } from "./client";

type Group = NonNullable<AdminPaths["/api/v1/groups"]["get"]["responses"][200]["content"]["application/json"]["data"]>[number];
type Application = NonNullable<AdminPaths["/api/v1/applications"]["get"]["responses"][200]["content"]["application/json"]["data"]>[number];
type Model = NonNullable<AdminPaths["/api/v1/models"]["get"]["responses"][200]["content"]["application/json"]["data"]>[number];

export const dynamic = "force-dynamic";

export default async function APIKeysPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string; unbound?: string }>;
}) {
  const { cursor, limit, unbound } = await searchParams;
  let props: ComponentProps<typeof APIKeysPageClient>;
  try {
    const client = await serverAdminClient();
    const page = unwrap(await client.GET("/api/v1/api-keys", {
      params: { query: { cursor, limit: limit ? Number(limit) : undefined, unbound: unbound === "true" || undefined } },
    }));
    const groups: { value: string; label: string }[] = [];
    const applications: { value: string; label: string }[] = [];
    const models: { value: string; label: string }[] = [];
    let groupCursor: string | undefined;
    do {
      const result = unwrap(await client.GET("/api/v1/groups", { params: { query: { limit: 500, cursor: groupCursor } } }));
      const rows: Group[] = result.data ?? [];
      groups.push(...rows.map((g) => ({ value: String(g.id), label: g.name ?? String(g.id) })));
      groupCursor = result.next_cursor || undefined;
    } while (groupCursor);
    let appCursor: string | undefined;
    do {
      const result = unwrap(await client.GET("/api/v1/applications", { params: { query: { limit: 500, cursor: appCursor } } }));
      const rows: Application[] = result.data ?? [];
      applications.push(...rows.filter((a) => a.enabled).map((a) => ({ value: String(a.id), label: a.name ?? String(a.id) })));
      appCursor = result.next_cursor || undefined;
    } while (appCursor);
    let modelCursor: string | undefined;
    do {
      const result = unwrap(await client.GET("/api/v1/models", { params: { query: { limit: 500, cursor: modelCursor } } }));
      const rows: Model[] = result.data ?? [];
      models.push(...rows.map((m) => ({ value: m.alias, label: m.alias })));
      modelCursor = result.next_cursor || undefined;
    } while (modelCursor);
    props = { rows: page.data ?? [], nextCursor: page.next_cursor ?? "", models, groups, applications, unbound: unbound === "true" };
  } catch (err) {
    const outcome = await handleAdminError(err);
    return <div className="p-8"><ForbiddenNotice message={outcome.message} /></div>;
  }
  return <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8"><APIKeysPageClient {...props} /></div>;
}
