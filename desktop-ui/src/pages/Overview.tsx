import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { getOverview } from "../lib/api";
import type { AgentUsage } from "../lib/types";
import { agentLabel, agentTone, formatDuration, formatNumber } from "../lib/format";

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
      <div className="mx-auto max-w-6xl p-6">
        <Skeleton className="h-8 w-40" />
        <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-28" />
          ))}
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="mx-auto max-w-6xl p-6">
        <EmptyState title="无法加载概览" description={error} />
      </div>
    );
  }

  const maxReq = Math.max(1, ...agents.map((a) => a.request_count));
  const maxTok = Math.max(1, ...agents.map((a) => a.total_tokens));
  const maxErr = Math.max(1, ...agents.map((a) => a.error_count));

  return (
    <div className="mx-auto max-w-6xl p-6">
      <h1 className="text-xl font-semibold">概览</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        各 Agent 的调用量、Token 与延迟汇总（成本为顺手记录的 Token 量，桌面版不跑计费）。
      </p>

      {totals && (
        <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Stat label="总调用" value={formatNumber(totals.request_count)} />
          <Stat label="总 Token" value={formatNumber(totals.total_tokens)} />
          <Stat label="总耗时" value={formatDuration(totals.duration_ms)} />
          <Stat label="错误" value={formatNumber(totals.error_count)} tone="destructive" />
        </div>
      )}

      <h2 className="mt-8 text-lg font-semibold">按 Agent</h2>
      {agents.length === 0 ? (
        <EmptyState className="mt-4" title="暂无数据" description="让任意 Agent 通过本网关发请求后即可看到统计。" />
      ) : (
        <div className="mt-3 grid gap-4 lg:grid-cols-2">
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
                  <Bar value={a.request_count} max={maxReq} tone="bg-primary" />
                </Metric>
                <Metric label="Token" value={formatNumber(a.total_tokens)}>
                  <Bar value={a.total_tokens} max={maxTok} tone="bg-info" />
                </Metric>
                <Metric label="错误" value={formatNumber(a.error_count)}>
                  <Bar value={a.error_count} max={maxErr} tone="bg-destructive" />
                </Metric>
                <div className="flex justify-between text-xs text-muted-foreground">
                  <span>平均耗时 {formatDuration(a.request_count ? a.duration_ms / a.request_count : 0)}</span>
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
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: "destructive" }) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="text-sm text-muted-foreground">{label}</div>
        <div className={`mt-1 text-2xl font-semibold ${tone === "destructive" ? "text-destructive" : ""}`}>
          {value}
        </div>
      </CardContent>
    </Card>
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
