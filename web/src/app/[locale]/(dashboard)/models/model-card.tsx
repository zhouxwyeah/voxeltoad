"use client";

import { useTranslations } from "next-intl";
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

export type CatalogModel = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

/**
 * Compact card for the models grid. Shows the essentials at a glance —
 * alias, description snippet, capability icons, context length, and a starting
 * price (cheapest upstream prompt rate). Full details live on the detail page.
 *
 * When `onEdit` / `onDelete` are provided (super-admin only), action buttons
 * render in the card header. Both handlers must call `e.preventDefault()` and
 * `e.stopPropagation()` so the surrounding `<Link>` to the detail page is not
 * triggered.
 */
export function ModelCard({
  model,
  onEdit,
  onDelete,
}: {
  model: CatalogModel;
  onEdit?: () => void;
  onDelete?: () => void;
}) {
  const t = useTranslations("models");

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

  // Stop click propagation so the surrounding <Link> doesn't navigate when the
  // button is clicked. The parent <Link> wraps the whole card.
  const stop = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };

  return (
    <div className="group relative flex h-full flex-col gap-3 rounded-lg border border-border bg-background p-4 transition-shadow hover:shadow-md">
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-semibold text-foreground">{model.alias}</h3>
        {onEdit || onDelete ? (
          <div className="flex shrink-0 gap-1 opacity-0 transition-opacity group-hover:opacity-100">
            {onEdit && (
              <button
                type="button"
                className="rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
                onClick={(e) => {
                  stop(e);
                  onEdit();
                }}
                aria-label={t("actions.edit")}
              >
                {t("actions.edit")}
              </button>
            )}
            {onDelete && (
              <button
                type="button"
                className="rounded px-1.5 py-0.5 text-[11px] text-destructive hover:bg-destructive/10"
                onClick={(e) => {
                  stop(e);
                  onDelete();
                }}
                aria-label={t("actions.delete")}
              >
                {t("actions.delete")}
              </button>
            )}
          </div>
        ) : upstreams.length > 0 ? (
          <span className="shrink-0 text-xs text-muted-foreground">
            {t("card.providers", { count: upstreams.length })}
          </span>
        ) : null}
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
