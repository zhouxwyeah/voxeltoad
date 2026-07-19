"use client";

import { useTranslations } from "next-intl";
import { useRouter } from "@/i18n/navigation";
import { Button } from "@/components/ui";
import { RANGE_OPTIONS, type OverviewRange } from "./range";

type OverviewData = Record<string, unknown>;

/**
 * Business dashboard: node count, recent error/block stats, top tenants,
 * plus a per-agent rollup driven by the time-range selector.
 * Read-only — super-admin only.
 */
export function OverviewPageClient({
  data,
  range,
}: {
  data: OverviewData;
  range: OverviewRange;
}) {
  const t = useTranslations("overview");

  const nodes = data.nodes as Record<string, unknown> | undefined;
  const stats = data.recent_stats as Record<string, unknown> | undefined;
  const topTenants = (data.top_tenants as Array<Record<string, unknown>>) ?? [];
  const agentStats = (data.agent_stats as Array<Record<string, unknown>>) ?? [];

  return (
    <>
      <div>
        <h1 className="text-xl font-semibold text-foreground">
          {t("heading")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      <RangeTabs range={range} />

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard
          label={t("cards.nodesOnline")}
          value={typeof nodes?.online === "number" ? String(nodes.online) : "—"}
        />
        <StatCard
          label={t("cards.totalRequests")}
          value={
            typeof stats?.total_requests === "number"
              ? String(stats.total_requests)
              : "—"
          }
        />
        <StatCard
          label={t("cards.errors")}
          value={
            typeof stats?.total_errors === "number"
              ? String(stats.total_errors)
              : "—"
          }
          warn={
            typeof stats?.total_errors === "number" &&
            (stats.total_errors as number) > 0
          }
        />
        <StatCard
          label={t("cards.blocked")}
          value={
            typeof stats?.total_blocked === "number"
              ? String(stats.total_blocked)
              : "—"
          }
          warn={
            typeof stats?.total_blocked === "number" &&
            (stats.total_blocked as number) > 0
          }
        />
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-2">
        <StatCard
          label={t("cards.tokensIn")}
          value={
            typeof stats?.total_tokens_in === "number"
              ? (stats.total_tokens_in as number).toLocaleString()
              : "—"
          }
          muted
        />
        <StatCard
          label={t("cards.tokensOut")}
          value={
            typeof stats?.total_tokens_out === "number"
              ? (stats.total_tokens_out as number).toLocaleString()
              : "—"
          }
          muted
        />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <AgentStatsTable rows={agentStats} />
        <TopTenantsTable rows={topTenants} />
      </div>
    </>
  );
}

function RangeTabs({ range }: { range: string }) {
  const router = useRouter();
  const t = useTranslations("overview");
  return (
    <div className="flex flex-wrap items-center gap-2">
      {RANGE_OPTIONS.map((r) => (
        <Button
          key={r}
          variant={r === range ? "primary" : "outline"}
          size="sm"
          onClick={() => router.push(`/overview?range=${r}`)}
        >
          {t(`range.${r}`)}
        </Button>
      ))}
    </div>
  );
}

function AgentStatsTable({ rows }: { rows: Array<Record<string, unknown>> }) {
  const t = useTranslations("overview");
  return (
    <div>
      <h2 className="mb-2 text-sm font-semibold text-foreground">
        {t("agentStats")}
      </h2>
      {rows.length === 0 ? (
        <EmptyRow label={t("agentStats")} />
      ) : (
        <table className="w-full border-collapse rounded-lg border border-border text-sm">
          <thead>
            <tr className="border-b border-border bg-muted text-left">
              <Th>{t("agentColumns.agent")}</Th>
              <Th className="text-right">{t("agentColumns.requests")}</Th>
              <Th className="text-right">{t("agentColumns.prompt")}</Th>
              <Th className="text-right">{t("agentColumns.completion")}</Th>
              <Th className="text-right">{t("agentColumns.total")}</Th>
              <Th className="text-right">{t("agentColumns.errors")}</Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => {
              const req = num(row.request_count);
              const prompt = num(row.prompt_tokens);
              const completion = num(row.completion_tokens);
              const total = num(row.total_tokens);
              const errors = num(row.error_count);
              const agent = (row.agent_type as string) || "—";
              return (
                <tr
                  key={agent + String(i)}
                  className="border-b border-border last:border-b-0 hover:bg-accent/50"
                >
                  <Td>{agent}</Td>
                  <Td className="text-right tabular-nums">{req.toLocaleString()}</Td>
                  <Td className="text-right tabular-nums">{prompt.toLocaleString()}</Td>
                  <Td className="text-right tabular-nums">
                    {completion.toLocaleString()}
                  </Td>
                  <Td className="text-right tabular-nums font-medium">
                    {total.toLocaleString()}
                  </Td>
                  <Td
                    className={`text-right tabular-nums ${
                      errors > 0 ? "text-destructive" : ""
                    }`}
                  >
                    {errors.toLocaleString()}
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

function TopTenantsTable({ rows }: { rows: Array<Record<string, unknown>> }) {
  const t = useTranslations("overview");
  if (rows.length === 0) return null;
  return (
    <div>
      <h2 className="mb-2 text-sm font-semibold text-foreground">
        {t("topTenants")}
      </h2>
      <table className="w-full border-collapse rounded-lg border border-border text-sm">
        <thead>
          <tr className="border-b border-border bg-muted text-left">
            <Th>{t("columns.tenant")}</Th>
            <Th className="text-right">{t("columns.requests")}</Th>
            <Th className="text-right">{t("columns.tokens")}</Th>
            <Th className="text-right">{t("columns.cost")}</Th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => {
            const prompt = num(row.prompt_tokens);
            const completion = num(row.completion_tokens);
            const cost = num(row.cost);
            return (
              <tr
                key={(row.group_key as string) ?? String(i)}
                className="border-b border-border last:border-b-0 hover:bg-accent/50"
              >
                <Td>{(row.group_key as string) ?? "—"}</Td>
                <Td className="text-right tabular-nums">
                  {num(row.request_count).toLocaleString()}
                </Td>
                <Td className="text-right tabular-nums">
                  {(prompt + completion).toLocaleString()}
                </Td>
                <Td className="text-right tabular-nums">
                  {cost.toLocaleString()}
                </Td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function EmptyRow({ label }: { label: string }) {
  return (
    <div className="rounded-lg border border-border bg-muted/30 p-4 text-sm text-muted-foreground">
      —
    </div>
  );
}

// num coerces an unknown JSON value to a finite number, defaulting to 0.
// API ints arrive as float64 in JSON; guard against null/undefined/string.
function num(v: unknown): number {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  return 0;
}

function Th({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <th
      className={`px-4 py-2 text-xs font-semibold uppercase text-muted-foreground ${className}`}
    >
      {children}
    </th>
  );
}

function Td({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return <td className={`px-4 py-2 ${className}`}>{children}</td>;
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
