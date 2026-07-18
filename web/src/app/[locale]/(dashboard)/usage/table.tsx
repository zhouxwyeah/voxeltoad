"use client";

import { useSearchParams } from "next/navigation";
import { useRouter } from "@/i18n/navigation";
import { useMemo } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { EmptyState } from "@/components/ui/empty-state";
import { microToDisplay } from "@/lib/money";

type UsageRow = Record<string, unknown>;

export function UsageTable({
  rows,
  nextCursor,
}: {
  rows: UsageRow[];
  nextCursor: string;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const t = useTranslations("usage");
  const tCommon = useTranslations("common");

  const columns: ColumnDef<UsageRow>[] = useMemo(
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
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
      },
      {
        accessorKey: "model",
        header: t("columns.model"),
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined;
          return v ?? <span className="text-muted-foreground">—</span>;
        },
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
        accessorKey: "prompt_tokens",
        header: t("columns.promptTokens"),
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
        accessorKey: "completion_tokens",
        header: t("columns.completionTokens"),
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
        accessorKey: "cost",
        header: t("columns.cost"),
        cell: ({ getValue }) => {
          const v = getValue() as number | undefined;
          if (v === undefined || v === null)
            return <span className="text-muted-foreground">—</span>;
          return (
            <span className="tabular-nums text-foreground">
              {microToDisplay(v)}
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

  function goNext() {
    const params = new URLSearchParams(searchParams.toString());
    params.set("cursor", nextCursor);
    router.push(`/usage?${params.toString()}`);
  }

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
      {nextCursor && (
        <div className="flex justify-end border-t border-border px-4 py-3">
          <Button variant="outline" size="sm" onClick={goNext}>
            {tCommon("actions.nextPage")}
          </Button>
        </div>
      )}
    </div>
  );
}
