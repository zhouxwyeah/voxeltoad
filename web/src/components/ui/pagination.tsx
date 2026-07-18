"use client";

import { useMemo } from "react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";

/**
 * Offset-pagination toolbar for the audit & request-logs pages.
 *
 * Left: page-size selector (10/20/50/100) + total record count.
 * Right: first / prev / windowed page numbers / next / last.
 *
 * Router-agnostic: the caller owns URL state and forwards changes via the
 * onPageChange / onPageSizeChange callbacks. totalPages is capped at maxPage
 * (mirrors the backend maxPage guard) so the UI never offers a deep-offset
 * page that the backend would clamp away.
 */
export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

export function Pagination({
  page,
  pageSize,
  total,
  maxPage = 500,
  onPageChange,
  onPageSizeChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  maxPage?: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: number) => void;
}) {
  const t = useTranslations("common.pagination");

  const totalPages = useMemo(
    () => Math.max(1, Math.min(Math.ceil(total / pageSize), maxPage)),
    [total, pageSize, maxPage],
  );
  const safePage = Math.min(Math.max(page, 1), totalPages);

  // Windowed page list: current page ±2, always clamped to [1, totalPages],
  // with ellipses where a gap exists. Produces a bounded, stable button row.
  const pages = useMemo(() => buildPageWindow(safePage, totalPages), [safePage, totalPages]);

  const onFirst = safePage > 1 ? () => onPageChange(1) : undefined;
  const onPrev = safePage > 1 ? () => onPageChange(safePage - 1) : undefined;
  const onNext = safePage < totalPages ? () => onPageChange(safePage + 1) : undefined;
  const onLast = safePage < totalPages ? () => onPageChange(totalPages) : undefined;

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-4 py-3">
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span className="tabular-nums">
          {t("total", { count: total.toLocaleString() })}
        </span>
        <label className="flex items-center gap-1.5">
          <select
            value={pageSize}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
            className="h-7 rounded border border-border bg-background px-1 text-xs text-foreground"
          >
            {PAGE_SIZE_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
          <span>{t("pageSize")}</span>
        </label>
      </div>

      <div className="flex items-center gap-1">
        <PagerButton label={t("firstPage")} disabled={!onFirst} onClick={onFirst} />
        <PagerButton label={t("prevPage")} disabled={!onPrev} onClick={onPrev} />
        {pages.map((p, i) =>
          p === "…" ? (
            <span key={`gap-${i}`} className="px-1 text-xs text-muted-foreground">
              …
            </span>
          ) : (
            <PagerButton
              key={p}
              label={String(p)}
              active={p === safePage}
              onClick={() => onPageChange(p)}
            />
          ),
        )}
        <PagerButton label={t("nextPage")} disabled={!onNext} onClick={onNext} />
        <PagerButton label={t("lastPage")} disabled={!onLast} onClick={onLast} />
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
