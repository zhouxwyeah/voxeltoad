"use client";

import { useMemo } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import { Pagination } from "@/components/ui/pagination";

type RequestLogRow = Record<string, unknown>;

/**
 * Request logs table with server-side offset pagination. The pagination
 * toolbar (page-size selector + page-jump) is driven by URL state owned by the
 * parent client; this component only renders rows and forwards page changes.
 */
export function RequestLogsTable({
  rows,
  total,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
}: {
  rows: RequestLogRow[];
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: number) => void;
}) {
  const t = useTranslations("request-logs");

  const columns: ColumnDef<RequestLogRow>[] = useMemo(
    () => [
      {
        accessorKey: "created_at",
        header: t("columns.time"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          if (!v) return <span className="text-muted-foreground">—</span>;
          return (
            <span className="whitespace-nowrap text-foreground">
              {new Date(v).toLocaleString()}
            </span>
          );
        },
      },
      {
        accessorKey: "tenant",
        header: t("columns.tenant"),
      },
      {
        accessorKey: "provider",
        header: t("columns.provider"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
      },
      {
        accessorKey: "model_requested",
        header: t("columns.modelRequested"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
      },
      {
        accessorKey: "total_tokens",
        header: t("columns.totalTokens"),
        cell: ({ getValue }) => {
          const v = getValue() as number | undefined;
          if (v === undefined || v === null)
            return <span className="text-muted-foreground">—</span>;
          return (
            <span className="tabular-nums text-foreground">
              {v.toLocaleString()}
            </span>
          );
        },
      },
      {
        accessorKey: "ttft_ms",
        header: t("columns.ttftMs"),
        cell: ({ getValue }) => {
          const v = getValue() as number | undefined;
          if (v === undefined || v === null)
            return <span className="text-muted-foreground">—</span>;
          return (
            <span className="tabular-nums text-foreground">
              {v.toLocaleString()}
            </span>
          );
        },
      },
      {
        accessorKey: "duration_ms",
        header: t("columns.durationMs"),
        cell: ({ getValue }) => {
          const v = getValue() as number | undefined;
          if (v === undefined || v === null)
            return <span className="text-muted-foreground">—</span>;
          return (
            <span className="tabular-nums text-foreground">
              {v.toLocaleString()}
            </span>
          );
        },
      },
      {
        accessorKey: "error_type",
        header: t("columns.errorType"),
        cell: ({ getValue, row }) => {
          const v = getValue() as string | undefined;
          const blocked = row.original.blocked_by as string | undefined;
          if (!v && !blocked)
            return (
              <span className="text-emerald-600 dark:text-emerald-400">
                {t("status.success")}
              </span>
            );
          return (
            <span className="text-destructive">
              {blocked ? `${t("columns.blockedBy")}: ${blocked}` : v}
            </span>
          );
        },
      },
      {
        accessorKey: "session_id",
        header: t("columns.session"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          if (!v) return <span className="text-muted-foreground">—</span>;
          return (
            <Link
              href={`/request-logs/sessions/${encodeURIComponent(v)}`}
              className="text-primary underline-offset-2 hover:underline"
            >
              {v.length > 12 ? `${v.slice(0, 10)}…` : v}
            </Link>
          );
        },
      },
    ],
    [t],
  );

  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-background">
      <table className="w-full border-collapse text-sm">
        <thead>
          {table.getHeaderGroups().map((hg) => (
            <tr
              key={hg.id}
              className="border-b border-border bg-muted text-left"
            >
              {hg.headers.map((h) => (
                <th
                  key={h.id}
                  className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
                >
                  {flexRender(h.column.columnDef.header, h.getContext())}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.length === 0 ? (
            <tr>
              <td
                colSpan={columns.length}
                className="px-4 py-10 text-center text-muted-foreground"
              >
                {t("actions.emptyState")}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map((row) => (
              <tr
                key={row.id}
                className="border-b border-border last:border-b-0 transition-colors hover:bg-accent/50"
              >
                {row.getVisibleCells().map((cell) => (
                  <td key={cell.id} className="px-4 py-2.5 text-foreground">
                    {flexRender(
                      cell.column.columnDef.cell,
                      cell.getContext(),
                    )}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
      <Pagination
        page={page}
        pageSize={pageSize}
        total={total}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
      />
    </div>
  );
}
