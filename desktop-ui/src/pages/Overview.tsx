import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { getOverview } from "../lib/api";
import type { AgentUsage } from "../lib/types";
import { agentLabel, agentTone, formatDuration, formatNumber } from "../lib/format";

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

export function Overview() {
  const [agents, setAgents] = useState<AgentUsage[]>([]);
  const [totals, setTotals] = useState<AgentUsage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  useEffect(() => {
    getOverview()
      .then((r) => {
        setAgents(r.agents);
        setTotals(r.totals);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-4 w-64" />
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <EmptyState title="无法加载概览" description={error} />
      </div>
    );
  }

  const maxReq = Math.max(1, ...agents.map((a) => a.request_count));
  const maxTok = Math.max(1, ...agents.map((a) => a.total_tokens));
  const maxErr = Math.max(1, ...agents.map((a) => a.error_count));

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">概览</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          各 Agent 的调用量、Token 与延迟汇总（成本为顺手记录的 Token 量，桌面版不跑计费）。
        </p>
      </div>

      {totals && (
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
          <StatCard label="总调用" value={formatNumber(totals.request_count)} />
          <StatCard label="总 Token" value={formatNumber(totals.total_tokens)} />
          <StatCard label="总耗时" value={formatDuration(totals.duration_ms)} />
          <StatCard label="错误" value={formatNumber(totals.error_count)} warn={totals.error_count > 0} />
        </div>
      )}

      <div>
        <h2 className="mb-2 text-sm font-semibold text-foreground">按 Agent</h2>
        {agents.length === 0 ? (
          <EmptyState
            title="暂无数据"
            description="让任意 Agent 通过本网关发请求后即可看到统计。"
          />
        ) : (
          <div className="grid gap-4 lg:grid-cols-2">
            {agents.map((a) => (
              <Card key={a.agent_type}>
                <CardHeader>
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-base">{agentLabel(a.agent_type)}</CardTitle>
                    <Badge tone={agentTone(a.agent_type)}>{a.agent_type}</Badge>
                  </div>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                  <Metric label="调用" value={formatNumber(a.request_count)}>
                    <Bar value={a.request_count} max={maxReq} tone="bg-primary/60" />
                  </Metric>
                  <Metric label="Token" value={formatNumber(a.total_tokens)}>
                    <Bar value={a.total_tokens} max={maxTok} tone="bg-primary/60" />
                  </Metric>
                  <Metric label="错误" value={formatNumber(a.error_count)}>
                    <Bar value={a.error_count} max={maxErr} tone="bg-destructive/60" />
                  </Metric>
                  <div className="flex justify-between text-xs text-muted-foreground">
                    <span>
                      平均耗时 {formatDuration(a.request_count ? a.duration_ms / a.request_count : 0)}
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
