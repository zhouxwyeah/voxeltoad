import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ModelsPageClient } from "./client";

export const dynamic = "force-dynamic";

type Pricing = {
  prompt_per_1m?: number;
  completion_per_1m?: number;
  currency?: string;
};

type ModelUpstream = {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing?: Pricing;
};

type ModelRow = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

type ProviderOption = { name: string };

export default async function ModelsPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: ModelRow[] = [];
  let providers: ProviderOption[] = [];
  let nextCursor = "";
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);
    const page = unwrap(
      await client.GET("/api/v1/models", { params: { query } }),
    );
    rows = (page.data ?? []) as ModelRow[];
    nextCursor = (page as { next_cursor?: string }).next_cursor ?? "";

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
      <ModelsPageClient
        rows={rows}
        providers={providers}
        nextCursor={nextCursor}
      />
    </div>
  );
}
