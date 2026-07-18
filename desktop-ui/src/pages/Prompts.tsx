import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ConfirmModal } from "../components/ui/confirm-modal";
import { EmptyState } from "../components/ui/empty-state";
import { Input } from "../components/ui/input";
import { Pagination } from "../components/ui/pagination";
import { Skeleton } from "../components/ui/skeleton";
import { PromptFormModal } from "../components/prompts/prompt-form-modal";
import { deletePrompt, listPrompts } from "../lib/api";
import type { PromptTemplate } from "../lib/types";
import { formatTime } from "../lib/format";

// Prompt favorites list (design/desktop.md §10.3-7): the "learn from good
// prompts" loop — favorited traces land here via the Trace viewer's 收藏
// button; rows are searchable by title/content and filterable by tag.
export function Prompts() {
  const [q, setQ] = useState("");
  const [tag, setTag] = useState("");
  const [rows, setRows] = useState<PromptTemplate[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<PromptTemplate | null>(null);
  const [deleting, setDeleting] = useState<number | null>(null);

  function refresh() {
    setLoading(true);
    listPrompts({ q, tag, page, page_size: pageSize })
      .then((r) => {
        setRows(r.data);
        setTotal(r.total);
        setError(null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }

  useEffect(refresh, [q, tag, page, pageSize]); // eslint-disable-line react-hooks/exhaustive-deps

  async function onCopy(row: PromptTemplate) {
    try {
      await navigator.clipboard.writeText(row.content);
      toast.success("内容已复制。");
    } catch {
      toast.error("复制失败。");
    }
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-8">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground">Prompt 收藏</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            在 Trace 查看器中收藏的好 prompt 集中在这里，支持搜索、标签筛选与复用。
          </p>
        </div>
        <Button variant="primary" onClick={() => setCreateOpen(true)}>
          新建收藏
        </Button>
      </div>

      <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-muted/30 p-3">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">搜索</span>
          <Input
            value={q}
            placeholder="标题或内容包含…"
            onChange={(e) => {
              setQ(e.target.value);
              setPage(1);
            }}
            className="w-64"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">标签</span>
          <Input
            value={tag}
            placeholder="精确匹配单个标签"
            onChange={(e) => {
              setTag(e.target.value);
              setPage(1);
            }}
            className="w-40"
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
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-24" />
          ))}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          title={q || tag ? "没有匹配的收藏" : "还没有收藏"}
          description={
            q || tag
              ? "调整搜索关键字或标签再试。"
              : "打开任意会话的 Trace，点击「收藏」按钮把有价值的 prompt 存到这里。"
          }
        />
      ) : (
        <>
          <div className="flex flex-col gap-3">
            {rows.map((row) => (
              <div key={row.id} className="rounded-lg border border-border bg-background p-4">
                <div className="flex items-center gap-2">
                  <h3 className="text-sm font-semibold text-foreground">{row.title}</h3>
                  {row.tags.map((t) => (
                    <Badge key={t} tone="muted">
                      {t}
                    </Badge>
                  ))}
                  <span className="ml-auto text-xs text-muted-foreground">
                    更新于 {formatTime(row.updated_at)}
                  </span>
                </div>
                <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/30 p-2 font-mono text-xs text-muted-foreground">
                  {row.content.length > 500 ? `${row.content.slice(0, 500)}…` : row.content}
                </pre>
                {row.note && <p className="mt-2 text-xs text-muted-foreground">备注：{row.note}</p>}
                <div className="mt-3 flex items-center gap-2">
                  {row.session_id && (
                    <Link
                      to={`/trace/${encodeURIComponent(row.session_id)}`}
                      className="text-xs text-primary underline-offset-2 hover:underline"
                    >
                      来源会话
                    </Link>
                  )}
                  <div className="ml-auto flex gap-2">
                    <Button variant="outline" size="sm" onClick={() => onCopy(row)}>
                      复制内容
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => setEditRow(row)}>
                      编辑
                    </Button>
                    <Button variant="destructive" size="sm" onClick={() => setDeleting(row.id)}>
                      删除
                    </Button>
                  </div>
                </div>
              </div>
            ))}
          </div>
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
        </>
      )}

      {/* Create */}
      <PromptFormModal open={createOpen} onClose={() => setCreateOpen(false)} onSaved={refresh} />
      {/* Edit */}
      <PromptFormModal
        open={editRow !== null}
        onClose={() => setEditRow(null)}
        onSaved={refresh}
        editRow={editRow}
      />
      {/* Delete */}
      <ConfirmModal
        open={deleting !== null}
        onCancel={() => setDeleting(null)}
        onConfirm={async () => {
          if (deleting === null) return;
          try {
            await deletePrompt(deleting);
            toast.success("已删除。");
            setDeleting(null);
            refresh();
          } catch (e) {
            toast.error(String((e as Error)?.message ?? e));
          }
        }}
        title="删除收藏"
        message="删除后不可恢复，确定删除这条收藏？"
        confirmLabel="删除"
      />
    </div>
  );
}
