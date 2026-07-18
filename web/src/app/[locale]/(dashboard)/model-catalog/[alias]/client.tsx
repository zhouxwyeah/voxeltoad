"use client";

import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import { Button } from "@/components/ui";
import { microToDisplay } from "@/lib/money";

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

/**
 * Full model detail view: description, capabilities, tags, context length, and
 * the complete upstream provider table with per-upstream pricing.
 */
export function ModelDetailClient({ model }: { model: CatalogModel | null }) {
  const t = useTranslations("model-catalog");

  if (!model) {
    return (
      <>
        <Button href="/model-catalog" variant="outline" size="sm">
          ← {t("detail.back")}
        </Button>
        <p className="text-sm text-muted-foreground">{t("notFound")}</p>
      </>
    );
  }

  const upstreams = model.upstreams ?? [];
  const capabilities = model.capabilities ?? [];
  const tags = model.tags ?? [];

  return (
    <>
      <Button href="/model-catalog" variant="outline" size="sm">
        ← {t("detail.back")}
      </Button>

      <div className="flex flex-col gap-2">
        <h1 className="text-xl font-semibold text-foreground">{model.alias}</h1>
        {model.context_length ? (
          <p className="text-sm text-muted-foreground">
            {t("detail.contextLength")}:{" "}
            <span className="font-medium text-foreground">
              {model.context_length.toLocaleString()}
            </span>
          </p>
        ) : null}
      </div>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-foreground">
          {t("detail.description")}
        </h2>
        <p className="text-sm text-foreground whitespace-pre-wrap">
          {model.description || t("card.noDescription")}
        </p>
      </section>

      {capabilities.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-semibold text-foreground">
            {t("detail.capabilities")}
          </h2>
          <div className="flex flex-wrap gap-1.5">
            {capabilities.map((c) => (
              <span
                key={c}
                className="inline-flex items-center rounded-full bg-muted px-2.5 py-0.5 text-xs text-foreground"
              >
                {c}
              </span>
            ))}
          </div>
        </section>
      )}

      {tags.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-semibold text-foreground">
            {t("detail.tags")}
          </h2>
          <div className="flex flex-wrap gap-1.5">
            {tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center rounded border border-border px-2 py-0.5 text-xs text-muted-foreground"
              >
                {tag}
              </span>
            ))}
          </div>
        </section>
      )}

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-foreground">
          {t("detail.upstreams")}
        </h2>
        <div className="overflow-hidden rounded-lg border border-border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <th className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.provider")}
                </th>
                <th className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.upstreamModel")}
                </th>
                <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.promptPrice")}
                </th>
                <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.completionPrice")}
                </th>
                <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.maxTokens")}
                </th>
              </tr>
            </thead>
            <tbody>
              {upstreams.length === 0 ? (
                <tr>
                  <td
                    colSpan={5}
                    className="px-4 py-6 text-center text-muted-foreground"
                  >
                    —
                  </td>
                </tr>
              ) : (
                upstreams.map((u, i) => (
                  <tr
                    key={`${u.provider}-${u.upstream_model}-${i}`}
                    className="border-b border-border last:border-b-0"
                  >
                    <td className="px-4 py-2 text-foreground">{u.provider}</td>
                    <td className="px-4 py-2 text-foreground">
                      {u.upstream_model}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums text-foreground">
                      {u.pricing?.prompt_per_1m !== undefined
                        ? microToDisplay(u.pricing.prompt_per_1m)
                        : "—"}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums text-foreground">
                      {u.pricing?.completion_per_1m !== undefined
                        ? microToDisplay(u.pricing.completion_per_1m)
                        : "—"}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums text-foreground">
                      {u.default_max_tokens
                        ? u.default_max_tokens.toLocaleString()
                        : "—"}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
