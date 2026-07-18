import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ModelDetailClient } from "./client";

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

type CatalogModel = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

export default async function ModelDetailPage({
  params,
}: {
  params: Promise<{ alias: string }>;
}) {
  const { alias } = await params;
  const decoded = decodeURIComponent(alias);

  let model: CatalogModel | null = null;
  try {
    const client = await serverAdminClient();
    const page = unwrap(
      await client.GET("/api/v1/models", { params: { query: { limit: 1000 } } }),
    );
    const all = (page.data ?? []) as CatalogModel[];
    model = all.find((m) => m.alias === decoded) ?? null;
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-4xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-6 p-8">
      <ModelDetailClient model={model} />
    </div>
  );
}
