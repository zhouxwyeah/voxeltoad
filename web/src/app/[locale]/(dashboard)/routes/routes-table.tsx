"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useMemo, useState } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { deleteRoute } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";

type RouteProvider = {
  name: string;
  weight?: number;
};

type RouteRow = {
  model_alias: string;
  providers?: RouteProvider[];
  strategy?: "priority" | "weighted" | "round_robin" | "session_affinity";
};

export function RoutesTable({
  rows,
  nextCursor,
  onView,
  onEdit,
}: {
  rows: RouteRow[];
  nextCursor: string;
  onView?: (row: RouteRow) => void;
  onEdit?: (row: RouteRow) => void;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tR = useTranslations("routes");

  const [deleteTarget, setDeleteTarget] = useState<RouteRow | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const columns: ColumnDef<RouteRow>[] = useMemo(
    () => [
      { accessorKey: "model_alias", header: tR("columns.modelAlias") },
      {
        id: "strategy",
        header: tR("columns.strategy"),
        cell: ({ row }) => {
          const strategy = row.original.strategy;
          if (!strategy) return <span className="text-muted-foreground">-</span>;
          return (
            <span className="inline-flex rounded-full bg-muted px-2 py-0.5 text-xs text-foreground">
              {tR(`strategy.${strategy}`)}
            </span>
          );
        },
      },
      {
        id: "providers",
        header: tR("columns.providers"),
        cell: ({ row }) => {
          const providers = row.original.providers ?? [];
          if (providers.length === 0) {
            return (
              <span className="text-muted-foreground">
                {tR("actions.noProviders")}
              </span>
            );
          }
          return (
            <div className="flex flex-col gap-1">
              {providers.map((p, i) => (
                <span
                  key={`${p.name}-${i}`}
                  className="inline-flex w-fit items-center rounded-full bg-muted px-2 py-0.5 text-xs text-foreground"
                >
                  {p.name}
                  {p.weight !== undefined && ` ·${p.weight}`}
                </span>
              ))}
            </div>
          );
        },
      },
    ],
    [tR],
  );

  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setDeleteError(null);
    const res = await deleteRoute(deleteTarget.model_alias);
    if (res.ok) {
      setDeleteTarget(null);
      router.refresh();
    } else {
      setDeleteError(res.error);
    }
    setDeleteLoading(false);
  }

  function goNext() {
    const params = new URLSearchParams(searchParams.toString());
    params.set("cursor", nextCursor);
    router.push(`/routes?${params.toString()}`);
  }

  return (
    <>
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
                <th className="w-0 px-4 py-2.5" />
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.length === 0 ? (
              <tr>
                <td
                  colSpan={columns.length + 1}
                  className="px-4 py-10 text-center text-muted-foreground"
                >
                  {tR("actions.emptyState")}
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row) => (
                <tr
                  key={row.id}
                  className="border-b border-border last:border-b-0 transition-colors hover:bg-accent/50"
                >
                  {row.getVisibleCells().map((cell) => (
                    <td
                      key={cell.id}
                      className="px-4 py-2.5 text-foreground align-top"
                    >
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                  <td className="px-4 py-2.5 text-right align-top">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => onView?.(row.original)}
                      >
                        {tR("actions.view")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => onEdit?.(row.original)}
                      >
                        {tCommon("actions.edit")}
                      </Button>
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => {
                          setDeleteTarget(row.original);
                          setDeleteError(null);
                        }}
                      >
                        {tCommon("actions.delete")}
                      </Button>
                    </div>
                  </td>
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

      <ConfirmModal
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={tCommon("modal.confirmDelete")}
        message={
          deleteTarget
            ? tR("actions.deleteConfirm", {
                modelAlias: deleteTarget.model_alias,
              })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
