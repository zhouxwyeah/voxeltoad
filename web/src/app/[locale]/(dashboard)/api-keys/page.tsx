import { redirect } from "next/navigation";
import { serverAdminClient } from "@/lib/admin";
import { onAuthExpired } from "@/lib/errors";
import { AdminError, unwrap } from "@voxeltoad/gateway-sdk/admin";
import { APIKeysPageClient } from "./client";

export const dynamic = "force-dynamic";

type ModelOption = { value: string; label: string };

export default async function APIKeysPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: Array<Record<string, unknown>> = [];
  let nextCursor = "";
  let models: ModelOption[] = [];
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    const page = unwrap(
      await client.GET("/api/v1/api-keys", { params: { query } }),
    );
    rows = (page.data ?? []) as Array<Record<string, unknown>>;
    nextCursor = page.next_cursor ?? "";
  } catch (err) {
    await onAuthExpired(err);
  }

  try {
    const client = await serverAdminClient();
    const modelsPage = unwrap(
      await client.GET("/api/v1/models", { params: { query: {} } }),
    );
    models = ((modelsPage.data ?? []) as { alias: string }[]).map((m) => ({
      value: m.alias,
      label: m.alias,
    }));
  } catch (err) {
    // A 401 means the back-end session died — our cookie still holds a
    // now-dead token. Bounce to /logout so it's cleared; this must not be
    // swallowed, or the user silently degrades to "no models" forever.
    if (err instanceof AdminError && err.status === 401) {
      redirect("/logout");
    }
    // Models fetch failure is non-blocking — key creation still works
    // without model restrictions (empty = allow all).
    console.error("[api-keys] failed to load models for selector:", err);
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <APIKeysPageClient rows={rows} nextCursor={nextCursor} models={models} />
    </div>
  );
}
