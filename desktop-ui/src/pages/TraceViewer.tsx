import { useEffect, useMemo, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { Tabs } from "../components/ui/tabs";
import { TraceCategories } from "../components/trace/trace-categories";
import { pickBaseline } from "../components/trace/trace-baseline";
import { JsonTree } from "../components/trace/json-tree";
import { PromptFormModal } from "../components/prompts/prompt-form-modal";
import { getTraceByRowID, listRequestLogs, listTraceBySession } from "../lib/api";
import type { RequestLogView, TraceDetail, TraceSummary } from "../lib/types";
import {
  agentLabel,
  agentTone,
  formatDuration,
  formatNumber,
  formatTime,
  shortId,
  statusTone,
} from "../lib/format";

// How many timeline rows above the selected one we inspect to find a good diff
// baseline. Subagent requests (Task/Explore) interleave with the main agent and
// share no message prefix (different system prompt), so we look back past them
// to find the same-branch ancestor. 10 covers typical Claude Code fan-out depth
// while staying cheap against the local desktop API + the row cache below.
const BASELINE_WINDOW = 10;

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export function TraceViewer() {
  const { sessionId = "" } = useParams();
  const navigate = useNavigate();
  const [timeline, setTimeline] = useState<TraceSummary[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [detail, setDetail] = useState<TraceDetail | null>(null);
  const [prevDetail, setPrevDetail] = useState<TraceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<string>("messages");

  useEffect(() => {
    setLoading(true);
    listTraceBySession(sessionId)
      .then((r) => {
        setTimeline(r.requests);
        setSelectedId(r.requests.length ? r.requests[0].id : null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, [sessionId]);

  // Candidate rows to score for the diff baseline: up to BASELINE_WINDOW rows
  // immediately above the selected one on the timeline. pickBaseline keeps the
  // one sharing the longest message prefix (skipping interleaved subagent
  // rows automatically); the closest same-branch ancestor wins.
  const prevIds = useMemo(() => {
    const idx = timeline.findIndex((t) => t.id === selectedId);
    if (idx <= 0) return [];
    const start = Math.max(0, idx - BASELINE_WINDOW);
    // Closest-first so pickBaseline's tie-break favours the nearest row.
    return timeline.slice(start, idx).map((t) => t.id).reverse();
  }, [timeline, selectedId]);

  // Small row-detail cache so clicking back and forth doesn't re-fetch rows
  // we already pulled. Bounded naturally — entries beyond the current window
  // just go unused; lifetime is this component instance, no manual eviction.
  const rowCache = useRef<Map<number, TraceDetail>>(new Map());

  useEffect(() => {
    if (selectedId == null) {
      setDetail(null);
      setPrevDetail(null);
      return;
    }
    let cancelled = false;
    setLoadingDetail(true);
    setActiveTab("messages");

    const cache = rowCache.current;
    // Current is always pulled fresh (it may have changed) and cached.
    const currentP = getTraceByRowID(selectedId).then((d) => {
      cache.set(selectedId, d);
      return d;
    });
    // Pull only candidates not already cached. Failures degrade to null so a
    // single broken row never blocks the baseline search or the detail view.
    const candPs = prevIds.map((id) => {
      const hit = cache.get(id);
      if (hit) return Promise.resolve(hit);
      return getTraceByRowID(id)
        .then((d) => {
          cache.set(id, d);
          return d;
        })
        .catch(() => null);
    });

    Promise.all([currentP, ...candPs])
      .then(([cur, ...cands]) => {
        if (cancelled) return;
        const prev = pickBaseline(cur, cands.filter((c): c is TraceDetail => c != null));
        setDetail(cur);
        setPrevDetail(prev);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(String(e?.message ?? e));
      })
      .finally(() => {
        if (!cancelled) setLoadingDetail(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedId, prevIds]);

  async function onCopy(key: string, text: string) {
    if (await copyText(text)) {
      setCopied(key);
      setTimeout(() => setCopied(null), 1500);
    }
  }

  if (loading) {
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
        <Skeleton className="h-7 w-64" />
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
        <EmptyState title="加载失败" description={error} />
      </div>
    );
  }

  if (timeline.length === 0) {
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
        <EmptyState
          title="该会话没有录制数据"
          description={`会话 ${shortId(sessionId, 16)} 暂无 trace 记录。`}
        />
      </div>
    );
  }

  return (
    <div className="flex h-full">
      {/* timeline — list items mirror the admin session-detail request list
          (border + primary active state); desktop keeps its richer per-item
          content (time, agent badge, model). */}
      <div className="w-80 shrink-0 overflow-auto border-r border-border p-3">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            请求时间线
          </h2>
          <Button variant="outline" size="sm" onClick={() => navigate("/sessions")}>
            <ArrowLeft className="h-3.5 w-3.5" />
            会话
          </Button>
        </div>
        <div className="flex flex-col gap-1">
          {timeline.map((t, i) => {
            const active = selectedId === t.id;
            return (
              <button
                key={t.id}
                onClick={() => setSelectedId(t.id)}
                className={`rounded-md border px-2 py-1.5 text-left text-sm transition-colors ${
                  active
                    ? "border-primary bg-primary/10"
                    : "border-transparent hover:bg-accent/40"
                }`}
              >
                <div className="flex items-center gap-2">
                  <span className="w-6 shrink-0 text-right tabular-nums text-muted-foreground">
                    #{i + 1}
                  </span>
                  <Badge tone={statusTone(t.status_code)}>{t.status_code}</Badge>
                  <span className="flex-1 truncate text-muted-foreground">
                    {t.model_requested || "—"}
                  </span>
                </div>
                <div className="mt-1 flex items-center gap-1.5 pl-8 text-xs text-muted-foreground">
                  <Badge tone={agentTone(t.agent_type)}>{agentLabel(t.agent_type)}</Badge>
                  <span className="truncate">{formatTime(t.created_at)}</span>
                </div>
              </button>
            );
          })}
        </div>
      </div>

      {/* detail */}
      <div className="flex-1 overflow-auto p-6">
        {loadingDetail || !detail ? (
          <Skeleton className="h-96 w-full" />
        ) : (
          <DetailView
            detail={detail}
            previous={prevDetail}
            activeTab={activeTab}
            onTabChange={setActiveTab}
            onCopy={onCopy}
            copied={copied}
          />
        )}
      </div>
    </div>
  );
}

function DetailView({
  detail,
  previous,
  activeTab,
  onTabChange,
  onCopy,
  copied,
}: {
  detail: TraceDetail;
  previous: TraceDetail | null;
  activeTab: string;
  onTabChange: (v: string) => void;
  onCopy: (key: string, text: string) => void;
  copied: string | null;
}) {
  // request_logs is asynchronously sinked; a trace row may exist before its
  // matching request_logs row. We accept a brief "—" state rather than
  // retrying (see design decision in plan).
  const [metrics, setMetrics] = useState<RequestLogView | null>(null);
  const [metricsLoading, setMetricsLoading] = useState(true);
  // Favorite modal: content prefilled from this trace row's messages.
  const [favoriteOpen, setFavoriteOpen] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setMetricsLoading(true);
    setMetrics(null);
    listRequestLogs({ request_id: detail.request_id, page_size: 1 })
      .then((r) => {
        if (cancelled) return;
        setMetrics(r.data[0] ?? null);
      })
      .catch(() => {
        if (cancelled) return;
        setMetrics(null);
      })
      .finally(() => {
        if (!cancelled) setMetricsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [detail.request_id]);

  const tabs = useMemo(() => {
    const list = [{ value: "messages", label: "消息" }, { value: "request", label: "请求体" }];
    if (detail.response_raw) list.push({ value: "response", label: "响应体" });
    if (detail.error_raw) list.push({ value: "error", label: "错误" });
    return list;
  }, [detail.response_raw, detail.error_raw]);

  // If response/error disappeared for this detail, snap back to messages.
  useEffect(() => {
    if (!tabs.some((t) => t.value === activeTab)) onTabChange("messages");
  }, [tabs, activeTab, onTabChange]);

  // 5xx/4xx is a failure path: tokens/TTFT/duration are usually 0/undefined
  // and "cache miss" is meaningless. Surface that as a visual anchor on the
  // header and "—" in the metrics bar instead of misleading zeros.
  const isError = detail.status_code >= 400;

  return (
    <div className="flex flex-col gap-4">
      {/* Header: status + agent + model + request_id + copy buttons */}
      <div
        className={`flex flex-wrap items-center gap-2 rounded-md py-1 pl-3 ${
          isError ? "border-l-4 border-destructive bg-destructive/5" : ""
        }`}
      >
        <Badge tone={statusTone(detail.status_code)}>HTTP {detail.status_code}</Badge>
        <Badge tone={agentTone(detail.agent_type)}>{agentLabel(detail.agent_type)}</Badge>
        <span className="text-sm text-muted-foreground">{detail.model_requested}</span>
        <span className="font-mono text-xs text-muted-foreground">{shortId(detail.request_id, 18)}</span>
        <div className="ml-auto flex gap-2">
          <Button size="sm" variant="outline" onClick={() => setFavoriteOpen(true)}>
            收藏
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => onCopy("prompt", JSON.stringify(detail.request_raw, null, 2))}
          >
            {copied === "prompt" ? "已复制" : "复制 prompt"}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => onCopy("raw", JSON.stringify(detail.messages, null, 2))}
          >
            {copied === "raw" ? "已复制" : "复制 messages"}
          </Button>
        </div>
      </div>

      {/* Metrics bar — from request_logs */}
      <MetricsBar metrics={metrics} loading={metricsLoading} isError={isError} />

      {/* Metadata card — fields the backend already returns but the UI previously hid */}
      <MetadataCard detail={detail} />

      {/* Tabs: messages / request / response / error */}
      <Tabs items={tabs} value={activeTab} onValueChange={onTabChange} />

      <div>
        {activeTab === "messages" && <TraceCategories current={detail} previous={previous} />}
        {activeTab === "request" && <JsonTree value={detail.request_raw} />}
        {activeTab === "response" && detail.response_raw && <JsonTree value={detail.response_raw} />}
        {activeTab === "error" && detail.error_raw && (
          <pre className="max-h-96 overflow-auto rounded-md border border-destructive/30 bg-destructive/5 p-3 text-xs text-destructive">
            {detail.error_raw}
          </pre>
        )}
      </div>

      {/* Favorite: prefill the messages JSON + provenance for this trace row */}
      <PromptFormModal
        open={favoriteOpen}
        onClose={() => setFavoriteOpen(false)}
        onSaved={() => setFavoriteOpen(false)}
        initial={{
          content: JSON.stringify(detail.messages, null, 2),
          session_id: detail.session_id,
          source_trace_row_id: detail.id,
        }}
      />
    </div>
  );
}

function MetricsBar({
  metrics,
  loading,
  isError,
}: {
  metrics: RequestLogView | null;
  loading: boolean;
  isError: boolean;
}) {
  // On error responses the numeric values are all zero / absent — render
  // them as "—" so the user reads "no data" rather than "0 tokens used".
  const m = isError ? null : metrics;
  const showLoading = loading && !isError;
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
      <Stat label="Prompt Tokens" value={showLoading ? "…" : m ? formatNumber(m.prompt_tokens) : "—"} />
      <Stat label="Completion Tokens" value={showLoading ? "…" : m ? formatNumber(m.completion_tokens) : "—"} />
      <Stat label="Total Tokens" value={showLoading ? "…" : m ? formatNumber(m.total_tokens) : "—"} />
      <Stat label="TTFT" value={showLoading ? "…" : m ? formatDuration(m.ttft_ms) : "—"} />
      <Stat label="Duration" value={showLoading ? "…" : m ? formatDuration(m.duration_ms) : "—"} />
      <Stat
        label="Cache"
        value={showLoading ? "…" : m ? (m.cache_hit ? m.cache_tier || "hit" : "miss") : "—"}
      />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  // Mirrors the admin session SummaryCard: rounded-lg border p-3, muted
  // label, large tabular value.
  return (
    <div className="rounded-lg border border-border p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-lg font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function MetadataCard({ detail }: { detail: TraceDetail }) {
  return (
    <dl className="grid gap-x-6 gap-y-4 rounded-lg border border-border p-4 sm:grid-cols-2 lg:grid-cols-3">
      <MetaItem label="Provider" value={detail.provider} mono />
      <MetaItem label="Stream" value={detail.stream ? "是" : "否"} />
      <MetaItem label="Stop Reason" value={detail.stop_reason || "—"} mono />
      <MetaItem label="Messages" value={String(detail.n_messages)} />
      <MetaItem label="Tool Uses" value={String(detail.n_tool_use)} />
      <MetaItem label="Tenant" value={detail.tenant || "—"} mono />
      <MetaItem label="Trace ID" value={shortId(detail.trace_id, 18)} mono />
      <MetaItem label="Created At" value={formatTime(detail.created_at)} />
    </dl>
  );
}

function MetaItem({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  // DetailField styling (design-system.md §3).
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{label}</dt>
      <dd className={`text-sm text-foreground ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  );
}
