import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";
import { Pagination } from "../components/ui/pagination";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { Button } from "../components/ui/button";
import { listSessions } from "../lib/api";
import type { SessionSummary } from "../lib/types";
import {
  agentLabel,
  agentTone,
  formatDuration,
  formatNumber,
  formatTime,
  shortId,
} from "../lib/format";

const AGENTS = ["claude-code", "codebuddy", "codex", "workbuddy", "opencode"];

function toRFC3339(date: string, endOfDay = false): string | undefined {
  if (!date) return undefined;
  return `${date}T${endOfDay ? "23:59:59" : "00:00:00"}Z`;
}

export function Sessions() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const [agent, setAgent] = useState(params.get("agent") ?? "");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [rows, setRows] = useState<SessionSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 20;

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
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, [agent, from, to, page]);

  return (
    <div className="mx-auto max-w-6xl p-6">
      <h1 className="text-xl font-semibold">会话浏览器</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        按 Agent 与时间段过滤，查看每个会话的请求序列（点开进入 Trace 查看器）。
      </p>

      <div className="mt-4 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Agent
          <Select value={agent} onChange={(e) => { setAgent(e.target.value); setPage(1); }}>
            <option value="">全部</option>
            {AGENTS.map((a) => (
              <option key={a} value={a}>
                {agentLabel(a)}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          起始日期
          <Input type="date" value={from} onChange={(e) => { setFrom(e.target.value); setPage(1); }} />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          结束日期
          <Input type="date" value={to} onChange={(e) => { setTo(e.target.value); setPage(1); }} />
        </label>
      </div>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle className="text-base">会话列表</CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <Skeleton className="h-40 w-full" />
          ) : error ? (
            <EmptyState title="加载失败" description={error} />
          ) : rows.length === 0 ? (
            <EmptyState title="暂无会话" description="满足条件的会话为空，调整过滤条件试试。" />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>会话 ID</TableHead>
                  <TableHead>Agent</TableHead>
                  <TableHead className="text-right">请求</TableHead>
                  <TableHead className="text-right">Token</TableHead>
                  <TableHead className="text-right">耗时</TableHead>
                  <TableHead>最近活跃</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((s) => (
                  <TableRow key={s.session_id}>
                    <TableCell className="font-mono">{shortId(s.session_id, 16)}</TableCell>
                    <TableCell>
                      <Badge tone={agentTone(s.agent_type)}>{agentLabel(s.agent_type)}</Badge>
                    </TableCell>
                    <TableCell className="text-right">{formatNumber(s.request_count)}</TableCell>
                    <TableCell className="text-right">{formatNumber(s.total_tokens)}</TableCell>
                    <TableCell className="text-right">{formatDuration(s.duration_ms)}</TableCell>
                    <TableCell className="text-muted-foreground">{formatTime(s.last_seen)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => navigate(`/trace/${encodeURIComponent(s.session_id)}`)}
                      >
                        查看
                      </Button>
                      {s.has_errors && <Badge tone="destructive" className="ml-2">有错误</Badge>}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {!loading && rows.length > 0 && (
            <div className="mt-3">
              <Pagination page={page} pageSize={pageSize} total={total} onPage={setPage} />
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
