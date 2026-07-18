"use client";

import { useMemo } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";

type NodeRow = Record<string, unknown>;

export function DataPlaneNodesTable({ rows }: { rows: NodeRow[] }) {
  const t = useTranslations("data-plane-nodes");

  const columns: ColumnDef<NodeRow>[] = useMemo(
    () => [
      {
        accessorKey: "instance_id",
        header: t("columns.instanceId"),
        cell: ({ getValue }) => (
          <span className="font-mono text-xs">{getValue() as string}</span>
        ),
      },
      { accessorKey: "hostname", header: t("columns.hostname") },
      {
        accessorKey: "addr",
        header: t("columns.addr"),
        cell: ({ getValue }) => (
          <code className="text-xs">{getValue() as string}</code>
        ),
      },
      {
        accessorKey: "version",
        header: t("columns.version"),
        cell: ({ getValue, row }) => {
          const ver = getValue() as string;
          const commit = row.original.commit as string;
          return (
            <div className="flex flex-col">
              <span className="font-mono text-xs">{ver}</span>
              {commit && commit !== "unknown" && (
                <span className="text-[10px] text-muted-foreground">
                  {commit.slice(0, 8)}
                </span>
              )}
            </div>
          );
        },
      },
      {
        accessorKey: "config_generation",
        header: t("columns.configGeneration"),
        cell: ({ getValue }) => {
          const v = getValue() as number;
          return (
            <span className="tabular-nums">{v?.toLocaleString() ?? "—"}</span>
          );
        },
      },
      {
        accessorKey: "status",
        header: t("columns.status"),
        cell: ({ getValue }) => {
          const s = (getValue() as string) ?? "unknown";
          const colors: Record<string, string> = {
            online: "text-success",
            offline: "text-destructive",
            draining: "text-warning",
          };
          return (
            <span className={colors[s] ?? ""}>
              {t(`status.${s}` as never) || s}
            </span>
          );
        },
      },
      {
        accessorKey: "started_at",
        header: t("columns.startedAt"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          if (!v) return <span className="text-muted-foreground">—</span>;
          return (
            <span className="whitespace-nowrap">
              {new Date(v).toLocaleString()}
            </span>
          );
        },
      },
      {
        accessorKey: "last_heartbeat_at",
        header: t("columns.lastHeartbeat"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          if (!v) return <span className="text-muted-foreground">—</span>;
          return (
            <span className="whitespace-nowrap">
              {new Date(v).toLocaleString()}
            </span>
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
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
