import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Check } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { Tabs } from "../components/ui/tabs";
import { EmptyState } from "../components/ui/empty-state";
import { getOverview, listModels, listProviders, listRoutes } from "../lib/api";
import type {
  AgentUsage,
  DimensionUsage,
  ErrorUsage,
  Model,
  OverviewResult,
  Provider,
  Route,
} from "../lib/types";
import {
  agentLabel,
  agentTone,
  formatDuration,
  formatNumber,
  formatPercent,
  formatTokens,
  microToDisplay,
} from "../lib/format";

const MICRO_PER_UNIT = 1_000_000;

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

function startOfWeekMonday(d: Date): Date {
  const x = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  x.setDate(x.getDate() - ((x.getDay() + 6) % 7));
  return x;
}

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

/** Estimate cost for a model alias from its upstream pricing. Uses the first
 * upstream's pricing (desktop is single-user; multi-upstream failover cost
 * attribution waits for DispatchStep). Returns micro-units (int64). */
function estimateModelCostMicro(model: Model | undefined, promptTokens: number, completionTokens: number): number {
  if (!model || !model.upstreams || model.upstreams.length === 0) return 0;
  const p = model.upstreams[0].pricing;
  if (!p) return 0;
  const promptCost = Math.round((promptTokens / MICRO_PER_UNIT) * p.prompt_per_1m);
  const completionCost = Math.round((completionTokens / MICRO_PER_UNIT) * p.completion_per_1m);
  return promptCost + completionCost;
}

export function Overview() {
  const [preset, setPreset] = useState<Preset>("today");
  const [data, setData] = useState<OverviewResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [setupState, setSetupState] = useState<{
    providers: Provider[];
    models: Model[];
    routes: Route[];
  } | null>(null);
  const navigate = useNavigate();

  const fetchData = useCallback((p: Preset) => {
    setRefreshing(true);
    const { from, to } = rangeFor(p, new Date());
    getOverview(from?.toISOString(), to?.toISOString())
      .then((r) => {
        setData(r);
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

  useEffect(() => {
    Promise.all([listProviders(), listModels(), listRoutes()])
      .then(([providers, models, routes]) => setSetupState({ providers, models, routes }))
      .catch(() => setSetupState(null));
  }, [tick]);

  if (loading) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-4 w-64" />
        <div className="grid grid-cols-2 gap-4 md:grid-cols-6">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      </div>
    );
  }

  if (error && !data) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <EmptyState title="无法加载概览" description={error} />
      </div>
    );
  }

  const agents = data?.agents ?? [];
  const totals = data?.totals;
  const providers = data?.providers ?? [];
  const models = data?.models ?? [];
  const errors = data?.errors ?? [];
  const scalars = data?.scalars;
  const modelConfigs = setupState?.models ?? [];

  const maxReq = Math.max(1, ...agents.map((a) => a.request_count));
  const maxErr = Math.max(1, ...agents.map((a) => a.error_count));
  const maxProvReq = Math.max(1, ...providers.map((p) => p.request_count));
  const maxModelReq = Math.max(1, ...models.map((m) => m.request_count));

  // SetupReadiness: Provider → Model → Route → Test.
  const setupSteps = setupState
    ? [
        {
          label: "添加供应商",
          done: setupState.providers.some(
            (p) => p.endpoints.length > 0 && p.api_key_ref && p.api_key_ref !== "plain://",
          ),
          to: "/providers",
        },
        {
          label: "创建模型",
          done: setupState.models.length > 0,
          to: "/models",
        },
        {
          label: "配置路由",
          done: setupState.routes.length > 0,
          to: "/routes",
        },
      ]
    : null;
  const setupComplete = setupSteps ? setupSteps.every((s) => s.done) : false;
  const showSetupCard = setupState && !setupComplete;

  // Total estimated cost across all models (micro-units → display).
  const totalCostMicro = models.reduce((sum, m) => {
    const cfg = modelConfigs.find((c) => c.alias === m.key);
    return sum + estimateModelCostMicro(cfg, m.prompt_tokens, m.completion_tokens);
  }, 0);
  const costCurrency = modelConfigs[0]?.upstreams[0]?.pricing?.currency ?? "";

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      {showSetupCard && setupSteps && <SetupCard steps={setupSteps} onNavigate={navigate} />}

      <div>
        <h1 className="text-xl font-semibold text-foreground">概览</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          请求级可靠性与用量汇总。{costCurrency && totalCostMicro > 0 && "成本为本地估算（不含缓存折扣）。"}
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

      {/* --- Scalars: 6 StatCards --- */}
      {scalars && totals && (
        <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
          <StatCard label="总调用" value={formatNumber(scalars.total_requests)} />
          <StatCard
            label="成功率"
            value={formatPercent(scalars.success_rate)}
            warn={scalars.success_rate < 1 && scalars.total_requests > 0}
          />
          <StatCard label="平均延迟" value={formatDuration(scalars.avg_duration_ms)} />
          <StatCard label="平均 TTFT" value={formatDuration(scalars.avg_ttft_ms)} />
          <StatCard label="输入 Token" value={formatTokens(totals.prompt_tokens)} />
          <StatCard label="输出 Token" value={formatTokens(totals.completion_tokens)} />
        </div>
      )}

      {/* --- Provider + Model distribution --- */}
      {(providers.length > 0 || models.length > 0) && (
        <div className="grid gap-4 lg:grid-cols-2">
          <DistributionCard
            title="供应商分布"
            items={providers}
            maxReq={maxProvReq}
            emptyText="暂无供应商维度的请求记录。"
          />
          <DistributionCard
            title="模型分布"
            items={models.map((m) => {
              const cfg = modelConfigs.find((c) => c.alias === m.key);
              const cost = estimateModelCostMicro(cfg, m.prompt_tokens, m.completion_tokens);
              return {
                ...m,
                suffix: cost > 0 ? `≈ ${costCurrency} ${microToDisplay(cost)}` : undefined,
              };
            })}
            maxReq={maxModelReq}
            emptyText="暂无模型维度的请求记录。"
            costLabel="估算"
          />
        </div>
      )}

      {/* --- Agent cards --- */}
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
                  <div className="grid grid-cols-3 gap-3 text-xs">
                    <div>
                      <span className="text-muted-foreground">输入</span>
                      <p className="font-medium">{formatTokens(a.prompt_tokens)}</p>
                    </div>
                    <div>
                      <span className="text-muted-foreground">输出</span>
                      <p className="font-medium">{formatTokens(a.completion_tokens)}</p>
                    </div>
                    <div>
                      <span className="text-muted-foreground">错误</span>
                      <p className={`font-medium ${a.error_count > 0 ? "text-destructive" : ""}`}>
                        {formatNumber(a.error_count)}
                      </p>
                    </div>
                  </div>
                  <Bar value={a.error_count} max={maxErr} tone="bg-destructive/60" />
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

      {/* --- Error type distribution --- */}
      {errors.length > 0 && (
        <div>
          <h2 className="mb-2 text-sm font-semibold text-foreground">错误类型分布</h2>
          <Card>
            <CardContent className="flex flex-col gap-2 pt-4">
              {errors.map((e) => (
                <ErrorRow key={e.error_type} err={e} total={scalars?.error_count ?? 0} />
              ))}
            </CardContent>
          </Card>
        </div>
      )}
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

function DistributionCard({
  title,
  items,
  maxReq,
  emptyText,
  costLabel,
}: {
  title: string;
  items: (DimensionUsage & { suffix?: string })[];
  maxReq: number;
  emptyText: string;
  costLabel?: string;
}) {
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle className="text-base">{title}</CardTitle>
          {costLabel && <span className="text-xs text-muted-foreground">{costLabel}</span>}
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {items.length === 0 ? (
          <p className="py-6 text-center text-xs text-muted-foreground">{emptyText}</p>
        ) : (
          items.map((d) => (
            <div key={d.key}>
              <div className="mb-1 flex items-center justify-between text-xs">
                <span className="font-medium text-foreground">{d.key}</span>
                <div className="flex items-center gap-2 text-muted-foreground">
                  {d.suffix && <span className="text-primary">{d.suffix}</span>}
                  <span>{formatNumber(d.request_count)} 次</span>
                  {d.error_count > 0 && <span className="text-destructive">{formatNumber(d.error_count)} 错</span>}
                </div>
              </div>
              <Bar value={d.request_count} max={maxReq} tone="bg-primary/60" />
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

function ErrorRow({ err, total }: { err: ErrorUsage; total: number }) {
  const pct = total > 0 ? Math.round((err.count / total) * 100) : 0;
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="font-medium text-foreground">{err.error_type}</span>
      <div className="flex items-center gap-3">
        <span className="text-muted-foreground">{formatNumber(err.count)} 次 · {pct}%</span>
      </div>
    </div>
  );
}

/** SetupReadiness card: shows a horizontal progress of Provider → Model → Route → Test. */
function SetupCard({
  steps,
  onNavigate,
}: {
  steps: { label: string; done: boolean; to: string }[];
  onNavigate: (to: string) => void;
}) {
  const nextStep = steps.find((s) => !s.done);
  const doneCount = steps.filter((s) => s.done).length;
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle className="text-base">配置向导</CardTitle>
          <span className="text-xs text-muted-foreground">{doneCount}/{steps.length + 1}</span>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center gap-2">
          {steps.map((s, i) => (
            <div key={s.label} className="flex flex-1 items-center gap-2">
              <div
                className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-medium ${
                  s.done
                    ? "bg-primary text-primary-foreground"
                    : nextStep === s
                      ? "border-2 border-primary text-primary"
                      : "border border-border text-muted-foreground"
                }`}
              >
                {s.done ? <Check className="h-4 w-4" /> : i + 1}
              </div>
              <span className={`text-sm ${s.done ? "text-muted-foreground line-through" : nextStep === s ? "font-medium text-foreground" : "text-muted-foreground"}`}>
                {s.label}
              </span>
              {i < steps.length - 1 && <div className={`h-px flex-1 ${s.done ? "bg-primary" : "bg-border"}`} />}
            </div>
          ))}
          <div className="flex items-center gap-2">
            <div
              className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-medium ${
                steps.every((s) => s.done)
                  ? "border-2 border-primary text-primary"
                  : "border border-border text-muted-foreground"
              }`}
            >
              {steps.length + 1}
            </div>
            <span className={`text-sm ${steps.every((s) => s.done) ? "font-medium text-foreground" : "text-muted-foreground"}`}>
              连通性测试
            </span>
          </div>
        </div>
        {nextStep ? (
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">下一步：{nextStep.label}</span>
            <Button size="sm" onClick={() => onNavigate(nextStep.to)}>
              前往
            </Button>
          </div>
        ) : (
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">配置就绪，发一个测试请求验证链路。</span>
            <Button size="sm" onClick={() => onNavigate("/playground")}>
              去测试
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
