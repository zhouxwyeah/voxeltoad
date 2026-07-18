import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { ModelCatalogClient } from "./client";

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

export default async function ModelCatalogPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string; capability?: string }>;
}) {
  const { q = "", capability = "" } = await searchParams;

  let models: CatalogModel[] = [];
  try {
    const client = await serverAdminClient();
    // Fetch all models for catalog browsing — models are a small global set
    // (tens, not thousands), so a single unbounded page is appropriate here.
    const page = unwrap(
      await client.GET("/api/v1/models", { params: { query: { limit: 1000 } } }),
    );
    models = (page.data ?? []) as CatalogModel[];
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
      <ModelCatalogClient models={models} query={q} capability={capability} />
    </div>
  );
}
