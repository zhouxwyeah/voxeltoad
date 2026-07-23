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
  providerAdapters = {},
}: {
  rows: RequestLogRow[];
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: number) => void;
  providerAdapters?: Record<string, string>;
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
        accessorKey: "ingress_protocol",
        header: t("columns.ingressProtocol"),
        cell: ({ row }) => {
          const proto = row.original.ingress_protocol as string | undefined;
          if (!proto) return <span className="text-muted-foreground">—</span>;
          // Protocol-match badge: the hit provider's adapter speaks the same
          // wire protocol as the ingress. This is an APPROXIMATION of
          // passthrough — protocol-aware routing prefers matched providers, so
          // a match usually means passthrough, but a request the adapter can't
          // fully express (e.g. Anthropic tool_use before the claude adapter
          // supports tools, ADR-0032 §5) may still be translated even on a
          // protocol match. The badge shows the routing fact, not the encoding
          // fact (which the gateway doesn't record per-request).
          const provider = row.original.provider as string | undefined;
          const adapter = provider
            ? (providerAdapters as Record<string, string>)[provider]
            : undefined;
          let passthrough: boolean | null = null;
          if (adapter) {
            passthrough =
              (proto === "anthropic" && adapter === "claude") ||
              (proto === "openai" && adapter === "openai");
          }
          return (
            <span className="flex items-center gap-1.5 whitespace-nowrap">
              <span className="text-foreground">{proto}</span>
              {passthrough !== null && (
                <span
                  title={t(
                    passthrough
                      ? "badges.passthroughHint"
                      : "badges.translatedHint",
                  )}
                  className={`rounded-full px-1.5 py-0.5 text-[10px] font-semibold ${
                    passthrough
                      ? "bg-success/10 text-success"
                      : "bg-muted text-muted-foreground"
                  }`}
                >
                  {passthrough
                    ? t("badges.passthrough")
                    : t("badges.translated")}
                </span>
              )}
            </span>
          );
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
              <span className="text-success">
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
