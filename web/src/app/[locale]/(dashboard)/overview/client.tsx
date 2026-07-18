"use client";

import { useTranslations } from "next-intl";

type OverviewData = Record<string, unknown>;

/**
 * Business dashboard: node count, recent error/block stats, top tenants.
 * Read-only — super-admin only.
 */
export function OverviewPageClient({ data }: { data: OverviewData }) {
  const t = useTranslations("overview");

  const nodes = data.nodes as Record<string, unknown> | undefined;
  const stats = data.recent_stats as Record<string, unknown> | undefined;
  const topTenants = (data.top_tenants as Array<Record<string, unknown>>) ?? [];

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

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard
          label={t("cards.nodesOnline")}
          value={typeof nodes?.online === "number" ? String(nodes.online) : "—"}
        />
        <StatCard
          label={t("cards.totalRequests")}
          value={typeof stats?.total_requests === "number" ? String(stats.total_requests) : "—"}
        />
        <StatCard
          label={t("cards.errors")}
          value={typeof stats?.total_errors === "number" ? String(stats.total_errors) : "—"}
          warn={typeof stats?.total_errors === "number" && (stats.total_errors as number) > 0}
        />
        <StatCard
          label={t("cards.blocked")}
          value={typeof stats?.total_blocked === "number" ? String(stats.total_blocked) : "—"}
          warn={typeof stats?.total_blocked === "number" && (stats.total_blocked as number) > 0}
        />
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-2">
        <StatCard
          label={t("cards.tokensIn")}
          value={typeof stats?.total_tokens_in === "number" ? (stats.total_tokens_in as number).toLocaleString() : "—"}
          muted
        />
        <StatCard
          label={t("cards.tokensOut")}
          value={typeof stats?.total_tokens_out === "number" ? (stats.total_tokens_out as number).toLocaleString() : "—"}
          muted
        />
      </div>

      {topTenants.length > 0 && (
        <div className="mt-4">
          <h2 className="mb-2 text-sm font-semibold text-foreground">
            {t("topTenants")}
          </h2>
          <table className="w-full border-collapse rounded-lg border border-border text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <th className="px-4 py-2 text-xs font-semibold uppercase text-muted-foreground">
                  {t("columns.tenant")}
                </th>
                <th className="px-4 py-2 text-xs font-semibold uppercase text-muted-foreground">
                  {t("columns.requests")}
                </th>
                <th className="px-4 py-2 text-xs font-semibold uppercase text-muted-foreground">
                  {t("columns.tokens")}
                </th>
                <th className="px-4 py-2 text-xs font-semibold uppercase text-muted-foreground">
                  {t("columns.cost")}
                </th>
              </tr>
            </thead>
            <tbody>
              {topTenants.map((row, i) => (
                <tr
                  key={(row.group_key as string) ?? i}
                  className="border-b border-border last:border-b-0 hover:bg-accent/50"
                >
                  <td className="px-4 py-2">{row.group_key as string}</td>
                  <td className="px-4 py-2 tabular-nums">
                    {String(row.request_count ?? 0)}
                  </td>
                  <td className="px-4 py-2 tabular-nums">
                    {((row.prompt_tokens as number) + (row.completion_tokens as number)).toLocaleString()}
                  </td>
                  <td className="px-4 py-2 tabular-nums">
                    {(row.cost as number)?.toLocaleString() ?? 0}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function StatCard({
  label,
  value,
  warn,
  muted,
}: {
  label: string;
  value: string;
  warn?: boolean;
  muted?: boolean;
}) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-border bg-background p-4">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span
        className={`text-2xl font-semibold tabular-nums ${
          warn ? "text-destructive" : muted ? "text-foreground" : "text-foreground"
        }`}
      >
        {value}
      </span>
    </div>
  );
}
