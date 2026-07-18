"use client";

import { useTranslations } from "next-intl";
import { microToDisplay } from "@/lib/money";

/**
 * Aggregate cards + Top-N breakdown (P1). The group_by dimension is chosen by
 * the operator; rows are already sorted by cost DESC by the backend. Money is
 * rendered via microToDisplay (int64 micro-units, no float drift — ADR-0013).
 */
export function UsageSummary({
  rows,
  groupBy,
}: {
  rows: Record<string, unknown>[];
  groupBy: string;
}) {
  const t = useTranslations("usage");

  const totalRequests = rows.reduce(
    (sum, r) => sum + Number(r.request_count ?? 0),
    0,
  );
  const totalPrompt = rows.reduce(
    (sum, r) => sum + Number(r.prompt_tokens ?? 0),
    0,
  );
  const totalCompletion = rows.reduce(
    (sum, r) => sum + Number(r.completion_tokens ?? 0),
    0,
  );
  const totalCost = rows.reduce(
    (sum, r) => sum + Number(r.cost ?? 0),
    0,
  );

  const cards = [
    { label: t("summary.totalRequests"), value: totalRequests.toLocaleString() },
    { label: t("summary.totalPromptTokens"), value: totalPrompt.toLocaleString() },
    { label: t("summary.totalCompletionTokens"), value: totalCompletion.toLocaleString() },
    { label: t("summary.totalCost"), value: microToDisplay(totalCost) },
  ];

  const top = [...rows].sort((a, b) => Number(b.cost ?? 0) - Number(a.cost ?? 0)).slice(0, 5);
  const maxCost = top.length > 0 ? Number(top[0].cost ?? 0) : 0;
  const groupByLabel = t(`filters.groupByOptions.${groupBy}`, { defaultMessage: groupBy });

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-semibold text-foreground">
          {t("summary.title")}
        </h2>
        <span className="text-[11px] text-muted-foreground">
          {t("summary.groupedBy")} {groupByLabel}
        </span>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map((card) => (
          <div
            key={card.label}
            className="rounded-lg border border-border bg-muted/30 p-4"
          >
            <p className="text-xs font-medium text-muted-foreground">
              {card.label}
            </p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-foreground">
              {card.value}
            </p>
          </div>
        ))}
      </div>
      {top.length > 0 && (
        <div className="rounded-lg border border-border bg-muted/20 p-4">
          <h3 className="mb-2 text-xs font-semibold text-foreground">
            {t("summary.topN", { count: top.length, dimension: groupByLabel })}
          </h3>
          <div className="flex flex-col gap-1.5">
            {top.map((row) => {
              const key = String(row.group_key ?? "");
              const cost = Number(row.cost ?? 0);
              const pct = maxCost > 0 ? (cost / maxCost) * 100 : 0;
              const reqCount = Number(row.request_count ?? 0);
              return (
                <div key={key} className="flex items-center gap-2 text-xs">
                  <span className="w-40 shrink-0 truncate font-mono text-foreground">
                    {key}
                  </span>
                  <div className="relative h-5 flex-1 rounded bg-background">
                    <div
                      className="absolute inset-y-0 left-0 rounded bg-primary/60"
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <span className="w-24 shrink-0 text-right tabular-nums text-muted-foreground">
                    {microToDisplay(cost)}
                  </span>
                  <span className="w-16 shrink-0 text-right tabular-nums text-muted-foreground">
                    {reqCount.toLocaleString()} {t("summary.requests")}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
