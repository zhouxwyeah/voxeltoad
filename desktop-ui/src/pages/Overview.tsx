import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { Tabs } from "../components/ui/tabs";
import { EmptyState } from "../components/ui/empty-state";
import { getOverview } from "../lib/api";
import type { AgentUsage } from "../lib/types";
import { agentLabel, agentTone, formatDuration, formatNumber, formatTokens } from "../lib/format";

// Data stays desktop-specific (per-agent usage from the local SQLite store);
// visuals mirror the admin overview/usage pages: StatCard style, muted
// breakdown bars, admin section-heading scale.
function Bar({ value, max, tone }: { value: number; max: number; tone: string }) {
  const pct = max > 0 ? Math.max(2, Math.round((value / max) * 100)) : 0;
  return (
    <div className="h-2 w-full rounded-full bg-muted">
      <div className={`h-2 rounded-full ${tone}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

/* ---------------------------------------------------------------------- */
/*  Time-range presets (local timezone; 本周从周一开始)                     */
/* ---------------------------------------------------------------------- */

type Preset = "today" | "yesterday" | "week" | "month" | "lastMonth" | "all";

const PRESETS: { value: Preset; label: string }[] = [
  { value: "today", label: "今天" },
  { value: "yesterday", label: "昨天" },
  { value: "week", label: "本周" },
  { value: "month", label: "本月" },
  { value: "lastMonth", label: "上月" },
  { value: "all", label: "全部" },
];

function presetLabel(p: Preset): string {
  return PRESETS.find((x) => x.value === p)?.label ?? p;
}

/** Monday 00:00 (local) of the week containing d. */
function startOfWeekMonday(d: Date): Date {
  const x = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  // getDay(): 0=Sun..6=Sat → days since Monday.
  x.setDate(x.getDate() - ((x.getDay() + 6) % 7));
  return x;
}

// rangeFor resolves a preset to a [from, to) window at fetch time, so
// open-ended presets (today/本周/本月) always extend to "now" on refresh.
function rangeFor(p: Preset, now: Date): { from?: Date; to?: Date } {
  const day0 = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  switch (p) {
    case "today":
      return { from: day0 };
    case "yesterday": {
      const y = new Date(day0);
      y.setDate(y.getDate() - 1);
      return { from: y, to: day0 };
    }
    case "week":
      return { from: startOfWeekMonday(now) };
    case "month":
      return { from: new Date(now.getFullYear(), now.getMonth(), 1) };
    case "lastMonth":
      return {
        from: new Date(now.getFullYear(), now.getMonth() - 1, 1),
        to: new Date(now.getFullYear(), now.getMonth(), 1),
      };
    case "all":
      return {};
  }
}

function rangeText(p: Preset, now: Date): string {
  const { from, to } = rangeFor(p, now);
  if (!from) return "全部时间";
  const fmtDay = (d: Date) =>
    d.toLocaleDateString("zh-CN", { year: "numeric", month: "numeric", day: "numeric" });
  return to ? `${fmtDay(from)} 至 ${fmtDay(to)}` : `${fmtDay(from)} 至今`;
}

export function Overview() {
  const [preset, setPreset] = useState<Preset>("today");
  const [agents, setAgents] = useState<AgentUsage[]>([]);
  const [totals, setTotals] = useState<AgentUsage | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const navigate = useNavigate();

  // loading gates the first-paint skeleton only; preset switches and manual
  // refreshes keep the previous data on screen and just flag `refreshing`.
  const fetchData = useCallback((p: Preset) => {
    setRefreshing(true);
    const { from, to } = rangeFor(p, new Date());
    getOverview(from?.toISOString(), to?.toISOString())
      .then((r) => {
        setAgents(r.agents);
        setTotals(r.totals);
        setError(null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, []);

  useEffect(() => {
    fetchData(preset);
  }, [preset, tick, fetchData]);

  if (loading) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-4 w-64" />
        <div className="grid grid-cols-2 gap-4 md:grid-cols-5">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      </div>
    );
  }

  if (error && !totals) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <EmptyState title="无法加载概览" description={error} />
      </div>
    );
  }

  const maxReq = Math.max(1, ...agents.map((a) => a.request_count));
  const maxInTok = Math.max(1, ...agents.map((a) => a.prompt_tokens));
  const maxOutTok = Math.max(1, ...agents.map((a) => a.completion_tokens));
  const maxErr = Math.max(1, ...agents.map((a) => a.error_count));

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">概览</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          各 Agent 的调用量、Token 与延迟汇总（成本为顺手记录的 Token 量，桌面版不跑计费）。
        </p>
      </div>

      <div className="flex flex-wrap items-end justify-between gap-2">
        <Tabs
          items={PRESETS}
          value={preset}
          onValueChange={(v) => setPreset(v as Preset)}
          variant="pill"
        />
        <div className="flex items-center gap-3 pb-1">
          {error && <span className="text-xs text-destructive">{error}</span>}
          <span className="text-xs text-muted-foreground">{rangeText(preset, new Date())}</span>
          <Button
            variant="outline"
            size="sm"
            disabled={refreshing}
            onClick={() => setTick((t) => t + 1)}
          >
            {refreshing ? "刷新中…" : "刷新"}
          </Button>
        </div>
      </div>

      {totals && (
        <div className="grid grid-cols-2 gap-4 md:grid-cols-5">
          <StatCard label="总调用" value={formatNumber(totals.request_count)} />
          <StatCard label="输入 Token" value={formatTokens(totals.prompt_tokens)} />
          <StatCard label="输出 Token" value={formatTokens(totals.completion_tokens)} />
          <StatCard label="总耗时" value={formatDuration(totals.duration_ms)} />
          <StatCard label="错误" value={formatNumber(totals.error_count)} warn={totals.error_count > 0} />
        </div>
      )}

      <div>
        <h2 className="mb-2 text-sm font-semibold text-foreground">按 Agent</h2>
        {agents.length === 0 ? (
          <EmptyState
            title="暂无数据"
            description={`「${presetLabel(preset)}」时间段内没有请求记录，让任意 Agent 通过本网关发请求后即可看到统计。`}
          />
        ) : (
          <div className="grid gap-4 lg:grid-cols-2">
            {agents.map((a) => (
              <Card key={a.agent_type}>
                <CardHeader>
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-base">{agentLabel(a.agent_type)}</CardTitle>
                    <Badge tone={agentTone(a.agent_type)}>{a.agent_type || "unknown"}</Badge>
                  </div>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                  <Metric label="调用" value={formatNumber(a.request_count)}>
                    <Bar value={a.request_count} max={maxReq} tone="bg-primary/60" />
                  </Metric>
                  <Metric label="输入 Token" value={formatTokens(a.prompt_tokens)}>
                    <Bar value={a.prompt_tokens} max={maxInTok} tone="bg-primary/60" />
                  </Metric>
                  <Metric label="输出 Token" value={formatTokens(a.completion_tokens)}>
                    <Bar value={a.completion_tokens} max={maxOutTok} tone="bg-primary/40" />
                  </Metric>
                  <Metric label="错误" value={formatNumber(a.error_count)}>
                    <Bar value={a.error_count} max={maxErr} tone="bg-destructive/60" />
                  </Metric>
                  <div className="flex justify-between text-xs text-muted-foreground">
                    <span>
                      平均耗时 {formatDuration(a.request_count ? a.duration_ms / a.request_count : 0)}
                      {" · "}
                      平均 TTFT {formatDuration(a.request_count ? a.ttft_ms / a.request_count : 0)}
                    </span>
                    <button
                      className="font-medium text-primary hover:underline"
                      onClick={() => navigate(`/sessions?agent=${encodeURIComponent(a.agent_type)}`)}
                    >
                      查看会话 →
                    </button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

/** Mirrors the admin overview StatCard. */
function StatCard({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-border bg-background p-4">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={`text-2xl font-semibold tabular-nums ${warn ? "text-destructive" : "text-foreground"}`}>
        {value}
      </span>
    </div>
  );
}

function Metric({ label, value, children }: { label: string; value: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 flex justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-medium">{value}</span>
      </div>
      {children}
    </div>
  );
}
