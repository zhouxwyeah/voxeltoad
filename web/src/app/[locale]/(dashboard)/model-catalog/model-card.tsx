"use client";

import { useTranslations } from "next-intl";
import { microToDisplay } from "@/lib/money";
import type { CatalogModel } from "./client";

/**
 * Compact card for the model catalog grid. Shows the essentials at a glance —
 * alias, description snippet, capability icons, context length, and a starting
 * price (cheapest upstream prompt rate). Full details live on the detail page.
 */
export function ModelCard({ model }: { model: CatalogModel }) {
  const t = useTranslations("model-catalog");

  const capabilities = model.capabilities ?? [];
  const upstreams = model.upstreams ?? [];

  // Starting price = lowest prompt_per_1m across upstreams.
  const startingPrice = upstreams
    .map((u) => u.pricing?.prompt_per_1m)
    .filter((v): v is number => typeof v === "number")
    .reduce((min, v) => (min === null || v < min ? v : min), null as number | null);

  const capabilityLabel = (c: string): string => {
    const known: Record<string, string> = {
      vision: t("capabilities.vision"),
      function_calling: t("capabilities.function_calling"),
      streaming: t("capabilities.streaming"),
      code: t("capabilities.code"),
    };
    return known[c] ?? c;
  };

  return (
    <div className="group flex h-full flex-col gap-3 rounded-lg border border-border bg-background p-4 transition-shadow hover:shadow-md">
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-semibold text-foreground">
          {model.alias}
        </h3>
        {upstreams.length > 0 && (
          <span className="shrink-0 text-xs text-muted-foreground">
            {t("card.providers", { count: upstreams.length })}
          </span>
        )}
      </div>

      <p className="line-clamp-2 text-xs text-muted-foreground">
        {model.description || t("card.noDescription")}
      </p>

      {capabilities.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {capabilities.slice(0, 4).map((c) => (
            <span
              key={c}
              className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-[11px] text-foreground"
            >
              {capabilityLabel(c)}
            </span>
          ))}
          {capabilities.length > 4 && (
            <span className="inline-flex items-center px-1 text-[11px] text-muted-foreground">
              +{capabilities.length - 4}
            </span>
          )}
        </div>
      )}

      <div className="mt-auto flex items-center justify-between border-t border-border pt-2 text-xs">
        {model.context_length ? (
          <span className="text-muted-foreground">
            {t("card.contextLength")}:{" "}
            <span className="font-medium text-foreground">
              {model.context_length.toLocaleString()}
            </span>
          </span>
        ) : (
          <span />
        )}
        {startingPrice !== null ? (
          <span className="text-muted-foreground">
            {t("card.startingPrice")}{" "}
            <span className="font-medium text-foreground">
              {microToDisplay(startingPrice)}
            </span>{" "}
            {t("card.perMillion")}
          </span>
        ) : (
          <span className="text-muted-foreground">{t("card.noPricing")}</span>
        )}
      </div>
    </div>
  );
}
