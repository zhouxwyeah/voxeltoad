"use client";

import { useTranslations } from "next-intl";
import { microToDisplay } from "@/lib/money";

export function UsageSummary({ rows, groupBy }: {
  rows: Record<string, unknown>[];
  groupBy: string;
}) {
  const t = useTranslations("usage");
  const cards = [
    { label: t("summary.totalRequests"), value: rows.reduce((sum, row) => sum + Number(row.request_count ?? 0), 0).toLocaleString() },
    { label: t("summary.totalPromptTokens"), value: rows.reduce((sum, row) => sum + Number(row.prompt_tokens ?? 0), 0).toLocaleString() },
    { label: t("summary.totalCompletionTokens"), value: rows.reduce((sum, row) => sum + Number(row.completion_tokens ?? 0), 0).toLocaleString() },
  ];
  const byCurrency = new Map<string, Record<string, unknown>[]>();
  for (const row of rows) {
    const currency = String(row.currency ?? "");
    const group = byCurrency.get(currency) ?? [];
    group.push(row);
    byCurrency.set(currency, group);
  }
  const groupByLabel = t(`filters.groupByOptions.${groupBy}`);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-semibold text-foreground">{t("summary.title")}</h2>
        <span className="text-[11px] text-muted-foreground">{t("summary.groupedBy")} {groupByLabel}</span>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        {cards.map((card) => (
          <div key={card.label} className="rounded-lg border border-border bg-muted/30 p-4">
            <p className="text-xs font-medium text-muted-foreground">{card.label}</p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-foreground">{card.value}</p>
          </div>
        ))}
      </div>
      {[...byCurrency].map(([currency, currencyRows]) => {
        const top = [...currencyRows].sort((a, b) => Number(b.cost ?? 0) - Number(a.cost ?? 0)).slice(0, 5);
        const maxCost = Number(top[0]?.cost ?? 0);
        const totalCost = currencyRows.reduce((sum, row) => sum + Number(row.cost ?? 0), 0);
        return (
          <div key={currency} className="rounded-lg border border-border bg-muted/20 p-4">
            <h3 className="mb-2 text-sm font-semibold text-foreground">
              {t("summary.totalCost")}: {currency ? `${microToDisplay(totalCost)} ${currency}` : t("identity.unknownCurrency")}
            </h3>
            <p className="mb-2 text-xs text-muted-foreground">{t("summary.topN", { count: top.length, dimension: groupByLabel })}</p>
            <div className="flex flex-col gap-1.5">
              {top.map((row) => {
                const key = String(row.group_key ?? "");
                const cost = Number(row.cost ?? 0);
                return (
                  <div key={key} className="flex items-center gap-2 text-xs">
                    <span className="w-40 shrink-0 truncate font-mono text-foreground">{key || t("identity.unattributed")}</span>
                    <div className="relative h-5 flex-1 rounded bg-background">
                      {currency && <div className="absolute inset-y-0 left-0 rounded bg-primary/60" style={{ width: `${maxCost > 0 ? cost / maxCost * 100 : 0}%` }} />}
                    </div>
                    <span className="w-32 shrink-0 text-right tabular-nums text-muted-foreground">{currency ? `${microToDisplay(cost)} ${currency}` : t("identity.unknownCurrency")}</span>
                    <span className="w-16 shrink-0 text-right tabular-nums text-muted-foreground">{Number(row.request_count ?? 0).toLocaleString()} {t("summary.requests")}</span>
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}
