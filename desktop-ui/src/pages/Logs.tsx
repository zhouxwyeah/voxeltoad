import { useEffect, useMemo, useState } from "react";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Skeleton } from "../components/ui/skeleton";
import { getLogs } from "../lib/api";

// Runtime log viewer over the gateway's in-memory ring buffer (stdlib +
// access logs teed into one stream). Polls every 3s; filtering is client-side
// over the fetched tail. Newest lines render on top so no scroll management
// is needed.
const TAIL_OPTIONS = [200, 500, 1000, 2000];
const POLL_MS = 3000;

export function Logs() {
  const [lines, setLines] = useState<string[]>([]);
  const [tail, setTail] = useState(500);
  const [filter, setFilter] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [initialLoading, setInitialLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    const fetchLogs = () => {
      getLogs(tail)
        .then((r) => {
          if (cancelled) return;
          setLines(r.lines ?? []);
          setError(null);
        })
        .catch((e) => {
          if (!cancelled) setError(String(e?.message ?? e));
        })
        .finally(() => {
          if (!cancelled) setInitialLoading(false);
        });
    };
    fetchLogs();
    const timer = setInterval(fetchLogs, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [tail]);

  // Newest first. Filtering keeps original (chronological) order per line.
  const visible = useMemo(() => {
    const kw = filter.trim().toLowerCase();
    const filtered = kw ? lines.filter((l) => l.toLowerCase().includes(kw)) : lines;
    return [...filtered].reverse();
  }, [lines, filter]);

  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col gap-4 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">运行日志</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          网关进程的启动、访问与错误日志（内存环形缓冲，每 3 秒自动刷新；完整历史见数据目录下的
          logs/desktop.log）。
        </p>
      </div>

      <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-muted/30 p-3">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">关键字过滤</span>
          <Input
            value={filter}
            placeholder="如 request_id / 500 / error"
            onChange={(e) => setFilter(e.target.value)}
            className="w-64"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">显示行数</span>
          <Select value={String(tail)} onChange={(e) => setTail(Number(e.target.value))}>
            {TAIL_OPTIONS.map((n) => (
              <option key={n} value={n}>
                最近 {n} 行
              </option>
            ))}
          </Select>
        </label>
        <span className="ml-auto text-xs text-muted-foreground">
          {filter ? `${visible.length} / ${lines.length} 行` : `${lines.length} 行`}
        </span>
      </div>

      {error && (
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      {initialLoading ? (
        <div className="space-y-1.5">
          {Array.from({ length: 12 }).map((_, i) => (
            <Skeleton key={i} className="h-4" />
          ))}
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-border bg-muted/20 p-3">
          {visible.length === 0 ? (
            <p className="py-10 text-center text-sm text-muted-foreground">
              {filter ? "没有匹配关键字的日志行。" : "暂无日志。"}
            </p>
          ) : (
            <pre className="font-mono text-xs leading-5 text-foreground">
              {visible.map((l, i) => (
                <div key={`${i}-${l.length}`} className="whitespace-pre-wrap break-all">
                  {l}
                </div>
              ))}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
