"use client";

import { useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui";
import { microToDisplay } from "@/lib/money";
import type { MetaRow, SessionStats, TraceRow } from "./page";
import type { TraceDetail } from "./[req]/detail-client";
import { fetchTraceDetailPair } from "./[req]/actions";
import { TraceCategories } from "@/components/trace/trace-categories";

type DetailPair = {
  current: TraceDetail | null;
  previous: TraceDetail | null;
};

/**
 * SessionDetailClient renders the 上+左右 layout with IN-PLACE right-panel
 * updates: clicking a request fetches its trace detail via a Server Action and
 * renders it into the right panel WITHOUT navigating.
 *
 * The request list is built from trace_payloads rows — each has a unique `id`.
 * This `id` is the fetch key (NOT request_id, which may be duplicated when a
 * client sends the same X-Request-Id for every request in a session). Token /
 * duration metadata from request_logs is merged by chronological position.
 */
export function SessionDetailClient({
  sessionID,
  traceRows,
  metaRows,
  stats,
  cost,
}: {
  sessionID: string;
  traceRows: TraceRow[];
  metaRows: MetaRow[];
  stats: SessionStats;
  cost?: number;
}) {
  const t = useTranslations("trace");

  // Build the request list from trace rows (unique id), merging token/duration
  // metadata from request_logs by chronological index (both are ASC by created_at).
  const requests = traceRows.map((tr, i) => {
    const meta = metaRows[i]; // same chronological position
    return {
      seq: i + 1,
      rowID: tr.id ?? 0,
      model: meta?.model_requested ?? "",
      status: tr.status_code ?? meta?.status_code ?? 0,
      durationMs: meta?.duration_ms ?? 0,
    };
  });

  const [selectedSeq, setSelectedSeq] = useState<number>(requests.length > 0 ? 1 : 0);
  const [panel, setPanel] = useState<
    | { status: "loading" }
    | { status: "loaded"; pair: DetailPair }
    | { status: "error" }
  >({ status: "loading" });

  async function loadSelected(rowID: number, previousRowID: number) {
    setPanel({ status: "loading" });
    try {
      const pair = await fetchTraceDetailPair(rowID, previousRowID);
      setPanel({ status: "loaded", pair });
    } catch (err) {
      console.error("[trace] loadSelected error", err);
      setPanel({ status: "error" });
    }
  }

  // Initial load on mount only.
  useEffect(() => {
    if (requests.length === 0 || !requests[0].rowID) return;
    loadSelected(requests[0].rowID, 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onSelect(seq: number) {
    if (seq === selectedSeq) return;
    setSelectedSeq(seq);
    const idx = seq - 1;
    const rowID = requests[idx]?.rowID ?? 0;
    const prevRowID = idx > 0 ? requests[idx - 1].rowID : 0;
    loadSelected(rowID, prevRowID);
  }

  return (
    <>
      <Button href="/trace" variant="outline" size="sm">
        ← {t("back")}
      </Button>

      {/* Top stats bar */}
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 rounded-lg border border-border bg-muted/30 px-4 py-3 text-sm">
        <div>
          <span className="text-muted-foreground">{t("sessions.session")}: </span>
          <span className="font-mono text-foreground">{sessionID}</span>
        </div>
        {stats.agent_type && (
          <div>
            <span className="text-muted-foreground">{t("detail.statsBar.agent")}: </span>
            <span className="text-foreground">{stats.agent_type}</span>
          </div>
        )}
        <div>
          <span className="text-muted-foreground">{t("detail.statsBar.startedAt")}: </span>
          <span className="text-foreground">
            {stats.started_at ? new Date(stats.started_at).toLocaleString() : "—"}
          </span>
        </div>
        <div>
          <span className="text-muted-foreground">{t("detail.statsBar.requestCount")}: </span>
          <span className="text-foreground tabular-nums">{stats.request_count}</span>
        </div>
        <div>
          <span className="text-muted-foreground">{t("detail.statsBar.inputTokens")}: </span>
          <span className="text-foreground tabular-nums">{stats.prompt_tokens}</span>
        </div>
        <div>
          <span className="text-muted-foreground">{t("detail.statsBar.outputTokens")}: </span>
          <span className="text-foreground tabular-nums">{stats.completion_tokens}</span>
        </div>
        <div>
          <span className="text-muted-foreground">{t("detail.statsBar.totalTokens")}: </span>
          <span className="text-foreground tabular-nums">{stats.total_tokens}</span>
        </div>
        {cost !== undefined && cost > 0 && (
          <div>
            <span className="text-muted-foreground">{t("sessions.cost")}: </span>
            <span className="text-foreground tabular-nums">{microToDisplay(cost)}</span>
          </div>
        )}
      </div>

      {/* Left/right split */}
      <div className="flex gap-4">
        {/* Left: request list */}
        <div className="w-72 shrink-0">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            {t("detail.requestList")}
          </h2>
          <div className="flex flex-col gap-1">
            {requests.map((r) => {
              const ok = r.status >= 200 && r.status < 300;
              const active = r.seq === selectedSeq;
              return (
                <button
                  key={r.seq}
                  type="button"
                  disabled={!r.rowID}
                  onClick={() => onSelect(r.seq)}
                  className={`flex items-center gap-2 rounded-md border px-2 py-1.5 text-left text-sm transition-colors ${
                    active
                      ? "border-primary bg-primary/10"
                      : "border-transparent hover:bg-accent/40"
                  }`}
                >
                  <span className="w-6 shrink-0 text-right tabular-nums text-muted-foreground">
                    #{r.seq}
                  </span>
                  <span
                    className={
                      ok
                        ? "text-emerald-600 dark:text-emerald-400"
                        : r.status
                          ? "text-destructive"
                          : "text-muted-foreground"
                    }
                  >
                    {r.status || "—"}
                  </span>
                  <span className="flex-1 truncate text-muted-foreground">
                    {r.model || "—"}
                  </span>
                  <span className="shrink-0 tabular-nums text-xs text-muted-foreground">
                    {r.durationMs ? `${(r.durationMs / 1000).toFixed(1)}s` : "—"}
                  </span>
                </button>
              );
            })}
            {requests.length === 0 && (
              <p className="text-sm text-muted-foreground">{t("notFound")}</p>
            )}
          </div>
        </div>

        {/* Right: selected request detail (in-place, no navigation) */}
        <div className="min-w-0 flex-1">
          <RightPanel panel={panel} t={t} />
        </div>
      </div>
    </>
  );
}

function RightPanel({
  panel,
  t,
}: {
  panel:
    | { status: "loading" }
    | { status: "loaded"; pair: DetailPair }
    | { status: "error" };
  t: ReturnType<typeof useTranslations>;
}) {
  if (panel.status === "loading") {
    return <p className="text-sm text-muted-foreground">{t("detail.loading")}</p>;
  }
  if (panel.status === "error") {
    return <p className="text-sm text-destructive">{t("detail.notFound")}</p>;
  }
  const { current, previous } = panel.pair;
  if (!current) {
    return <p className="text-sm text-muted-foreground">{t("detail.notFound")}</p>;
  }
  return <TraceCategories current={current} previous={previous} t={t} />;
}
