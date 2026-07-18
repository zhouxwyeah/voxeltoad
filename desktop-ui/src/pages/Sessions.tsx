import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { CircleAlert } from "lucide-react";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Pagination } from "../components/ui/pagination";
import { Skeleton } from "../components/ui/skeleton";
import { listSessions } from "../lib/api";
import type { SessionSummary } from "../lib/types";
import { agentLabel, formatDurationCompact, formatTime, formatTokens } from "../lib/format";

// Mirrors the admin trace landing page (web/.../(dashboard)/trace): session
// link with an error marker, agent type, request count, token total with
// prompt/completion detail, compact duration, started-at. Desktop keeps its
// date-range filters (supported by the desktop API) and shows no cost column
// (the desktop store intentionally does not track billing).
const AGENTS = ["claude-code", "codex", "codebuddy", "workbuddy", "opencode"];

function toRFC3339(date: string, endOfDay = false): string | undefined {
  if (!date) return undefined;
  return `${date}T${endOfDay ? "23:59:59" : "00:00:00"}Z`;
}

export function Sessions() {
  const [params] = useSearchParams();
  const [agent, setAgent] = useState(params.get("agent") ?? "");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [rows, setRows] = useState<SessionSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    listSessions({
      agent_type: agent,
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
  }, [agent, from, to, page, pageSize]);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">会话浏览器</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          按 Agent 与时间段过滤，查看每个会话的请求序列（点开进入 Trace 查看器）。
        </p>
      </div>

      {/* Filter bar (admin request-logs filter-bar styling) */}
      <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-muted/30 p-3">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">Agent</span>
          <Select
            value={agent}
            onChange={(e) => {
              setAgent(e.target.value);
              setPage(1);
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
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">起始日期</span>
          <Input
            type="date"
            value={from}
            onChange={(e) => {
              setFrom(e.target.value);
              setPage(1);
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
              setPage(1);
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
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-10" />
          ))}
        </div>
      ) : (
        /* Table shell + in-shell Pagination, mirroring admin request-logs. */
        <div className="overflow-hidden rounded-lg border border-border bg-background">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <Th>会话</Th>
                <Th>Agent 类型</Th>
                <Th className="text-right">请求数</Th>
                <Th className="text-right">Token 总量</Th>
                <Th className="text-right">耗时</Th>
                <Th>开始时间</Th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-10 text-center text-muted-foreground">
                    暂无会话。调整过滤条件，或先通过网关发起请求。
                  </td>
                </tr>
              ) : (
                rows.map((s) => (
                  <tr
                    key={s.session_id}
                    className="border-b border-border transition-colors last:border-b-0 hover:bg-accent/40"
                  >
                    <td className="px-3 py-2">
                      <Link
                        to={`/trace/${encodeURIComponent(s.session_id)}`}
                        className="font-mono text-primary underline-offset-2 hover:underline"
                      >
                        {s.session_id}
                      </Link>
                      {s.has_errors && (
                        <CircleAlert className="ml-2 inline h-3.5 w-3.5 text-destructive" />
                      )}
                    </td>
                    <td className="px-3 py-2">{agentLabel(s.agent_type)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{s.request_count}</td>
                    <td className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                      <span className="text-foreground">{formatTokens(s.total_tokens)}</span>
                      <span className="ml-1 text-xs">
                        ({formatTokens(s.prompt_tokens)}/{formatTokens(s.completion_tokens)})
                      </span>
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      {formatDurationCompact(s.duration_ms)}
                    </td>
                    <td className="px-3 py-2">{formatTime(s.started_at)}</td>
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

function Th({ children, className = "" }: { children: React.ReactNode; className?: string }) {
  return (
    <th
      className={`px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground ${className}`}
    >
      {children}
    </th>
  );
}
