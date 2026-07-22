import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Pagination } from "../components/ui/pagination";
import { Skeleton } from "../components/ui/skeleton";
import { listRequestLogs } from "../lib/api";
import type { RequestLogView } from "../lib/types";
import {
  agentLabel,
  formatDurationCompact,
  formatTime,
  formatTokens,
  shortId,
} from "../lib/format";

// Mirrors the admin request-logs page (advanced search + offset pagination),
// minus the multi-tenant columns (desktop is single-user) and plus the Agent
// dimension the desktop product is built around. Session cells link into the
// Trace viewer, matching the sessions page.
const AGENTS = ["claude-code", "codex", "codebuddy", "workbuddy", "opencode"];

function toRFC3339(date: string, endOfDay = false): string | undefined {
  if (!date) return undefined;
  return `${date}T${endOfDay ? "23:59:59" : "00:00:00"}Z`;
}

export function RequestLogs() {
  const [agent, setAgent] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [errorType, setErrorType] = useState("");
  const [sessionID, setSessionID] = useState("");
  const [requestID, setRequestID] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [rows, setRows] = useState<RequestLogView[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    listRequestLogs({
      agent_type: agent,
      provider,
      model_requested: model,
      error_type: errorType,
      session_id: sessionID,
      request_id: requestID,
      from: toRFC3339(from),
      to: toRFC3339(to, true),
      page,
      page_size: pageSize,
    })
      .then((r) => {
        setRows(r.data);
        setTotal(r.total);
        setError(null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, [agent, provider, model, errorType, sessionID, requestID, from, to, page, pageSize]);

  const resetPage = () => setPage(1);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">请求日志</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          每一次经过网关的请求审计（仅元数据，不含报文内容；报文见会话对应的 Trace）。
        </p>
      </div>

      {/* Filter bar (same idiom as the sessions page) */}
      <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-muted/30 p-3">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">Agent</span>
          <Select
            value={agent}
            onChange={(e) => {
              setAgent(e.target.value);
              resetPage();
            }}
          >
            <option value="">全部</option>
            {AGENTS.map((a) => (
              <option key={a} value={a}>
                {agentLabel(a)}
              </option>
            ))}
          </Select>
        </label>
        <FilterInput label="供应商" value={provider} onChange={(v) => { setProvider(v); resetPage(); }} placeholder="如 深度求索" />
        <FilterInput label="模型别名" value={model} onChange={(v) => { setModel(v); resetPage(); }} placeholder="如 deepseek-v4-flash" />
        <FilterInput label="错误类型" value={errorType} onChange={(v) => { setErrorType(v); resetPage(); }} placeholder="如 upstream_error" />
        <FilterInput label="会话 ID" value={sessionID} onChange={(v) => { setSessionID(v); resetPage(); }} />
        <FilterInput label="请求 ID" value={requestID} onChange={(v) => { setRequestID(v); resetPage(); }} />
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">起始日期</span>
          <Input
            type="date"
            value={from}
            onChange={(e) => {
              setFrom(e.target.value);
              resetPage();
            }}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">结束日期</span>
          <Input
            type="date"
            value={to}
            onChange={(e) => {
              setTo(e.target.value);
              resetPage();
            }}
          />
        </label>
      </div>

      {error && (
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-9" />
          ))}
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border bg-background">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <Th>时间</Th>
                <Th>Agent</Th>
                <Th>供应商</Th>
                <Th>模型</Th>
                <Th className="text-right">Tokens</Th>
                <Th className="text-right">TTFT</Th>
                <Th className="text-right">耗时</Th>
                <Th>状态</Th>
                <Th>会话</Th>
                <Th>请求 ID</Th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 ? (
                <tr>
                  <td colSpan={10} className="px-4 py-10 text-center text-muted-foreground">
                    暂无请求日志。调整过滤条件，或先通过网关发起请求。
                  </td>
                </tr>
              ) : (
                rows.map((r) => (
                  <tr
                    key={r.id}
                    className="border-b border-border transition-colors last:border-b-0 hover:bg-accent/40"
                  >
                    <td className="whitespace-nowrap px-3 py-2">{formatTime(r.created_at)}</td>
                    <td className="px-3 py-2">{r.agent_type ? agentLabel(r.agent_type) : "—"}</td>
                    <td className="px-3 py-2">{r.provider || "—"}</td>
                    <td className="px-3 py-2">
                      <span>{r.model_requested}</span>
                      {r.model_resolved && r.model_resolved !== r.model_requested && (
                        <span className="ml-1 text-xs text-muted-foreground">→ {r.model_resolved}</span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                      <span className="text-foreground">{formatTokens(r.total_tokens)}</span>
                      <span className="ml-1 text-xs">
                        ({formatTokens(r.prompt_tokens)}/{formatTokens(r.completion_tokens)})
                      </span>
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      {r.ttft_ms > 0 ? formatDurationCompact(r.ttft_ms) : "—"}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      {formatDurationCompact(r.duration_ms)}
                    </td>
                    <td className="px-3 py-2">
                      {r.error_type || r.blocked_by ? (
                        <span className="text-destructive">
                          {r.blocked_by ? `拦截: ${r.blocked_by}` : r.error_type}
                        </span>
                      ) : (
                        <span className="text-success">成功</span>
                      )}
                      {r.fallback && <span className="ml-1 text-xs text-warning">已回退</span>}
                    </td>
                    <td className="px-3 py-2">
                      {r.session_id ? (
                        <Link
                          to={`/trace/${encodeURIComponent(r.session_id)}`}
                          className="font-mono text-primary underline-offset-2 hover:underline"
                          title={r.session_id}
                        >
                          {shortId(r.session_id)}
                        </Link>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td
                      className="px-3 py-2 font-mono text-xs text-muted-foreground"
                      title={`gateway: ${r.request_id}${r.client_request_id ? `\nclient: ${r.client_request_id}` : ""}${r.upstream_request_id ? `\nupstream: ${r.upstream_request_id}` : ""}`}
                    >
                      {shortId(r.request_id)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
          <Pagination
            page={page}
            pageSize={pageSize}
            total={total}
            onPage={setPage}
            onPageSize={(s) => {
              setPageSize(s);
              setPage(1);
            }}
          />
        </div>
      )}
    </div>
  );
}

function FilterInput({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <Input
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="w-36"
      />
    </label>
  );
}

function Th({ children, className = "" }: { children: React.ReactNode; className?: string }) {
  return (
    <th
      className={`px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground ${className}`}
    >
      {children}
    </th>
  );
}
