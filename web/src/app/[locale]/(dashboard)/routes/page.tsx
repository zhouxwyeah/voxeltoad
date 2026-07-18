import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { RoutesPageClient } from "./client";

export const dynamic = "force-dynamic";

type RouteProvider = {
  name: string;
  weight?: number;
};

type RouteRow = {
  model_alias: string;
  providers?: RouteProvider[];
  strategy?: "priority" | "weighted" | "round_robin" | "session_affinity";
};

type ModelOption = {
  alias: string;
  upstreams?: { provider: string; upstream_model: string }[];
};

type ProviderOption = { name: string };

export default async function RoutesPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: RouteRow[] = [];
  let nextCursor = "";
  let models: ModelOption[] = [];
  let providers: ProviderOption[] = [];
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);

    const page = unwrap(
      await client.GET("/api/v1/routes", { params: { query } }),
    );
    rows = (page.data ?? []) as RouteRow[];
    nextCursor = page.next_cursor ?? "";

    const modelPage = unwrap(
      await client.GET("/api/v1/models", { params: { query: {} } }),
    );
    models = ((modelPage.data ?? []) as ModelOption[]).map((m) => ({
      alias: m.alias,
      upstreams: m.upstreams,
    }));

    const providerPage = unwrap(
      await client.GET("/api/v1/providers", { params: { query: {} } }),
    );
    providers = ((providerPage.data ?? []) as { name: string }[]).map(
      (p) => ({ name: p.name }),
    );
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
      <RoutesPageClient
        rows={rows}
        nextCursor={nextCursor}
        models={models}
        providers={providers}
      />
    </div>
  );
}
