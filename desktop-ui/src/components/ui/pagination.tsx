import { useMemo } from "react";
import { cn } from "../../lib/cn";
import { Select } from "./select";

// Pagination — offset-pagination toolbar mirroring web's shared
// src/components/ui/pagination.tsx: page-size selector + total on the left,
// first/prev/windowed pages/next/last on the right. Designed to sit inside
// the Table shell (hence the border-t), like admin's request-logs page.
export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

export function Pagination({
  page,
  pageSize,
  total,
  maxPage = 500,
  onPage,
  onPageSize,
}: {
  page: number;
  pageSize: number;
  total: number;
  maxPage?: number;
  onPage: (p: number) => void;
  onPageSize: (s: number) => void;
}) {
  const totalPages = useMemo(
    () => Math.max(1, Math.min(Math.ceil(total / pageSize), maxPage)),
    [total, pageSize, maxPage],
  );
  const safePage = Math.min(Math.max(page, 1), totalPages);
  const pages = useMemo(() => buildPageWindow(safePage, totalPages), [safePage, totalPages]);

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-4 py-3">
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span className="tabular-nums">共 {total.toLocaleString()} 条</span>
        <label className="flex items-center gap-1.5">
          <Select
            value={pageSize}
            onChange={(e) => onPageSize(Number(e.target.value))}
            className="h-7 w-auto rounded px-1 text-xs shadow-none"
            aria-label="每页条数"
          >
            {PAGE_SIZE_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
          <span>条 / 页</span>
        </label>
      </div>

      <div className="flex items-center gap-1">
        <PagerButton label="首页" disabled={safePage <= 1} onClick={() => onPage(1)} />
        <PagerButton label="上一页" disabled={safePage <= 1} onClick={() => onPage(safePage - 1)} />
        {pages.map((p, i) =>
          p === "…" ? (
            <span key={`gap-${i}`} className="px-1 text-xs text-muted-foreground">
              …
            </span>
          ) : (
            <PagerButton key={p} label={String(p)} active={p === safePage} onClick={() => onPage(p)} />
          ),
        )}
        <PagerButton
          label="下一页"
          disabled={safePage >= totalPages}
          onClick={() => onPage(safePage + 1)}
        />
        <PagerButton
          label="末页"
          disabled={safePage >= totalPages}
          onClick={() => onPage(totalPages)}
        />
      </div>
    </div>
  );
}

/** Small page / nav button with an active state. */
function PagerButton({
  label,
  active,
  disabled,
  onClick,
}: {
  label: string;
  active?: boolean;
  disabled?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 min-w-7 items-center justify-center rounded border border-border px-2 text-xs text-foreground transition-colors",
        "hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        "disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent",
        active && "border-primary bg-primary text-primary-foreground hover:bg-primary",
      )}
    >
      {label}
    </button>
  );
}

/**
 * Build a windowed page list with ellipses: e.g. for current=7, total=20 →
 * [1, "…", 5, 6, 7, 8, 9, "…", 20]. Always shows the first and last page.
 */
function buildPageWindow(current: number, total: number): (number | "…")[] {
  if (total <= 7) {
    return Array.from({ length: total }, (_, i) => i + 1);
  }
  const radius = 2;
  const start = Math.max(2, current - radius);
  const end = Math.min(total - 1, current + radius);
  const window: (number | "…")[] = [1];
  if (start > 2) window.push("…");
  for (let p = start; p <= end; p++) window.push(p);
  if (end < total - 1) window.push("…");
  window.push(total);
  return window;
}
