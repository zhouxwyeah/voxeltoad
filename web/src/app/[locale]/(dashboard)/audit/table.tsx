"use client";

import { useMemo } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { Pagination } from "@/components/ui/pagination";

type AuditRow = Record<string, unknown>;

export function AuditTable({
  rows,
  total,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
}: {
  rows: AuditRow[];
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: number) => void;
}) {
  const t = useTranslations("audit");

  const columns: ColumnDef<AuditRow>[] = useMemo(
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
        accessorKey: "operator_id",
        header: t("columns.operator"),
        cell: ({ getValue }) => {
          const v = getValue() as number | undefined;
          if (v === undefined || v === null)
            return <span className="text-muted-foreground">—</span>;
          return <span className="text-foreground">{v}</span>;
        },
      },
      {
        accessorKey: "tenant",
        header: t("columns.tenant"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
      },
      {
        accessorKey: "resource_type",
        header: t("columns.resourceType"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
      },
      {
        accessorKey: "resource_id",
        header: t("columns.resourceId"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
      },
      {
        accessorKey: "action",
        header: t("columns.action"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          if (!v) return <span className="text-muted-foreground">—</span>;
          const variant =
            v === "delete"
              ? "destructive"
              : v === "create"
                ? "success"
                : v === "read"
                  ? "secondary"
                  : "info";
          return (
            <Badge variant={variant}>
              {t(`actions.${v as "create" | "update" | "delete" | "read"}`)}
            </Badge>
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
              <td colSpan={columns.length}>
                <EmptyState title={t("actions.emptyState")} />
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
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
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
