"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { Link } from "@/i18n/navigation";
import { Input } from "@/components/ui";
import { EmptyState } from "@/components/ui/empty-state";
import { ModelCard } from "./model-card";

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

export type CatalogModel = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

/**
 * Model catalog: read-only card grid browsable by all authenticated operators.
 * Filtering (search + capability) is URL-driven for shareable/bookmarkable
 * views, following the convention used by request-logs/usage.
 */
export function ModelCatalogClient({
  models,
  query,
  capability,
}: {
  models: CatalogModel[];
  query: string;
  capability: string;
}) {
  const t = useTranslations("model-catalog");
  const tCommon = useTranslations("common");
  const router = useRouter();
  const searchParams = useSearchParams();

  // Collect the union of all capabilities across models for the filter chips.
  const allCapabilities = useMemo(() => {
    const set = new Set<string>();
    for (const m of models) {
      for (const c of m.capabilities ?? []) set.add(c);
    }
    return Array.from(set).sort();
  }, [models]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return models.filter((m) => {
      if (capability && !(m.capabilities ?? []).includes(capability)) return false;
      if (!q) return true;
      const haystack = [
        m.alias,
        m.description ?? "",
        ...(m.tags ?? []),
      ]
        .join(" ")
        .toLowerCase();
      return haystack.includes(q);
    });
  }, [models, query, capability]);

  function applyParam(key: string, value: string) {
    const params = new URLSearchParams(searchParams.toString());
    if (value) params.set(key, value);
    else params.delete(key);
    router.push(`/model-catalog?${params.toString()}`);
  }

  return (
    <>
      <div>
        <h1 className="text-xl font-semibold text-foreground">
          {t("heading")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("subtitle")}
        </p>
      </div>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex-1">
          <Input
            type="search"
            placeholder={t("search.placeholder")}
            defaultValue={query}
            onChange={(e) => applyParam("q", e.target.value)}
          />
        </div>
        {allCapabilities.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-muted-foreground">
              {t("filters.capability")}:
            </span>
            <button
              type="button"
              onClick={() => applyParam("capability", "")}
              className={`rounded-full px-2.5 py-0.5 text-xs transition-colors ${
                !capability
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-foreground hover:bg-accent"
              }`}
            >
              {t("filters.allCapabilities")}
            </button>
            {allCapabilities.map((c) => {
              const known: Record<string, string> = {
                vision: t("capabilities.vision"),
                function_calling: t("capabilities.function_calling"),
                streaming: t("capabilities.streaming"),
                code: t("capabilities.code"),
              };
              return (
                <button
                  key={c}
                  type="button"
                  onClick={() => applyParam("capability", c)}
                  className={`rounded-full px-2.5 py-0.5 text-xs transition-colors ${
                    capability === c
                      ? "bg-primary text-primary-foreground"
                      : "bg-muted text-foreground hover:bg-accent"
                  }`}
                >
                  {known[c] ?? c}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {filtered.length === 0 ? (
        <EmptyState title={t("empty")} />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {filtered.map((m) => (
            <Link
              key={m.alias}
              href={`/model-catalog/${encodeURIComponent(m.alias)}`}
              className="block"
            >
              <ModelCard model={m} />
            </Link>
          ))}
        </div>
      )}

      <p className="sr-only">{tCommon("appName")}</p>
    </>
  );
}
