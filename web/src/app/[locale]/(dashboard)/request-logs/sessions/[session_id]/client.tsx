"use client";

import { useTranslations } from "next-intl";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui";
import { microToDisplay } from "@/lib/money";

type RequestEntry = Record<string, unknown>;

type CostSummary = {
  session_id?: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  cost?: number;
  request_count?: number;
};

/**
 * Session trace client: renders a summary card (request count, tokens, cost,
 * errors, fallbacks, time span) and a chronological request timeline table.
 */
export function SessionTraceClient({
  sessionID,
  requests,
  costSummary,
  loadError,
}: {
  sessionID: string;
  requests: RequestEntry[];
  costSummary: CostSummary;
  loadError: boolean;
}) {
  const t = useTranslations("session-trace");

  if (loadError) {
    return (
      <>
        <Button href="/request-logs" variant="outline" size="sm">
          <ArrowLeft className="h-3.5 w-3.5" />
        {t("back")}
        </Button>
        <p className="text-sm text-destructive">{t("notFound")}</p>
      </>
    );
  }

  const count = requests.length;
  const errorCount = requests.filter(
    (r) => !!r.error_type || !!r.blocked_by,
  ).length;
  const fallbackCount = requests.filter((r) => r.fallback === true).length;

  // Time span between first and last request.
  const times = requests
    .map((r) => r.created_at as string | undefined)
    .filter((v): v is string => !!v)
    .map((v) => new Date(v).getTime());
  const spanMs =
    times.length >= 2 ? Math.max(...times) - Math.min(...times) : 0;

  const totalTokens =
    (costSummary.prompt_tokens ?? 0) + (costSummary.completion_tokens ?? 0);

  return (
    <>
      <Button href="/request-logs" variant="outline" size="sm">
        <ArrowLeft className="h-3.5 w-3.5" />
        {t("back")}
      </Button>

      <div className="flex flex-col gap-2">
        <h1 className="text-xl font-semibold text-foreground">
          {t("heading")}
        </h1>
        <p className="text-sm text-muted-foreground">
          <span className="font-mono">{sessionID}</span>
        </p>
        <p className="text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      {count === 0 ? (
        <p className="text-sm text-muted-foreground">{t("notFound")}</p>
      ) : (
        <>
          {/* Summary cards */}
          <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
            <SummaryCard label={t("summary.requestCount")} value={String(count)} />
            <SummaryCard
              label={t("summary.totalTokens")}
              value={totalTokens.toLocaleString()}
            />
            <SummaryCard
              label={t("summary.cost")}
              value={microToDisplay(costSummary.cost ?? 0)}
            />
            <SummaryCard
              label={t("summary.errors")}
              value={String(errorCount)}
              warn={errorCount > 0}
            />
            <SummaryCard
              label={t("summary.fallbacks")}
              value={String(fallbackCount)}
              warn={fallbackCount > 0}
            />
            <SummaryCard
              label={t("summary.timeSpan")}
              value={formatDuration(spanMs)}
            />
          </div>

          {/* Timeline table */}
          <div className="flex flex-col gap-2">
            <h2 className="text-sm font-semibold text-foreground">
              {t("timeline.title")}
            </h2>
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse text-sm">
                <thead>
                  <tr className="border-b border-border bg-muted text-left">
                    <Th>{t("timeline.time")}</Th>
                    <Th>{t("timeline.provider")}</Th>
                    <Th>{t("timeline.model")}</Th>
                    <Th className="text-right">{t("timeline.tokens")}</Th>
                    <Th className="text-right">{t("timeline.ttft")}</Th>
                    <Th className="text-right">{t("timeline.duration")}</Th>
                    <Th>{t("timeline.status")}</Th>
                  </tr>
                </thead>
                <tbody>
                  {requests.map((r, i) => {
                    const hasError = !!r.error_type || !!r.blocked_by;
                    return (
                      <tr
                        key={(r.request_id as string) ?? i}
                        className="border-b border-border last:border-b-0"
                      >
                        <Td>
                          {r.created_at
                            ? new Date(r.created_at as string).toLocaleString()
                            : "—"}
                        </Td>
                        <Td>{(r.provider as string) ?? "—"}</Td>
                        <Td>{(r.model_requested as string) ?? "—"}</Td>
                        <Td className="text-right tabular-nums">
                          {(r.total_tokens as number)?.toLocaleString() ?? "—"}
                        </Td>
                        <Td className="text-right tabular-nums">
                          {(r.ttft_ms as number)?.toLocaleString() ?? "—"}
                        </Td>
                        <Td className="text-right tabular-nums">
                          {(r.duration_ms as number)?.toLocaleString() ?? "—"}
                        </Td>
                        <Td>
                          {hasError ? (
                            <span className="text-destructive">
                              {r.blocked_by
                                ? `${t("timeline.fallback")}: ${r.blocked_by}`
                                : (r.error_type as string)}
                            </span>
                          ) : r.fallback === true ? (
                            <span className="text-warning">
                              {t("timeline.fallback")}
                            </span>
                          ) : (
                            <span className="text-success">
                              {t("timeline.success")}
                            </span>
                          )}
                        </Td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
    </>
  );
}

function SummaryCard({
  label,
  value,
  warn,
}: {
  label: string;
  value: string;
  warn?: boolean;
}) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-border bg-background p-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span
        className={`text-lg font-semibold tabular-nums ${
          warn ? "text-destructive" : "text-foreground"
        }`}
      >
        {value}
      </span>
    </div>
  );
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
      className={`px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground ${className}`}
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
  return (
    <td className={`px-3 py-2 text-foreground ${className}`}>{children}</td>
  );
}

function formatDuration(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  if (ms < 3_600_000) return `${(ms / 60_000).toFixed(1)}m`;
  return `${(ms / 3_600_000).toFixed(1)}h`;
}
