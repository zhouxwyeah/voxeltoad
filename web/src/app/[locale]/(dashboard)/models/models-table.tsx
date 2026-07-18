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
import { deleteModel } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";
import { microToDisplay } from "@/lib/money";

type Pricing = {
  prompt_per_1m?: number;
  completion_per_1m?: number;
  currency?: string;
  cache_hit_multiplier?: number;
};

type ModelUpstream = {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing?: Pricing;
};

type ModelRow = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

/**
 * Models table + delete ConfirmModal. No expanding rows / sub-table for
 * upstreams (out of first-cut scope) — instead each upstream renders as a
 * small inline pill within the Upstreams column.
 */
export function ModelsTable({
  rows,
  nextCursor,
  onEdit,
}: {
  rows: ModelRow[];
  nextCursor: string;
  onEdit?: (row: ModelRow) => void;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tM = useTranslations("models");

  const [deleteTarget, setDeleteTarget] = useState<ModelRow | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  function goNext() {
    const params = new URLSearchParams(searchParams.toString());
    params.set("cursor", nextCursor);
    router.push(`/models?${params.toString()}`);
  }

  const columns: ColumnDef<ModelRow>[] = useMemo(
    () => [
      { accessorKey: "alias", header: tM("columns.alias") },
      {
        id: "upstreams",
        header: tM("columns.upstreams"),
        cell: ({ row }) => {
          const ups = row.original.upstreams ?? [];
          if (ups.length === 0) {
            return (
              <span className="text-muted-foreground">
                {tM("actions.noUpstreams")}
              </span>
            );
          }
          return (
            <div className="flex flex-col gap-1">
              {ups.map((u, i) => (
                <span
                  key={`${u.provider}-${u.upstream_model}-${i}`}
                  className="inline-flex w-fit items-center rounded-full bg-muted px-2 py-0.5 text-xs text-foreground"
                >
                  {u.provider} · {u.upstream_model}
                  {u.pricing &&
                    ` · ${microToDisplay(u.pricing.prompt_per_1m ?? 0)}/${microToDisplay(u.pricing.completion_per_1m ?? 0)} per 1M`}
                  {u.pricing?.cache_hit_multiplier
                    ? ` · cache ${(u.pricing.cache_hit_multiplier / 10_000).toString()}%`
                    : ""}
                </span>
              ))}
            </div>
          );
        },
      },
    ],
    [tM],
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
    const res = await deleteModel(deleteTarget.alias);
    if (res.ok) {
      setDeleteTarget(null);
      router.refresh();
    } else {
      setDeleteError(res.error);
    }
    setDeleteLoading(false);
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
                  {tM("actions.emptyState")}
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
                      {onEdit && (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => onEdit(row.original)}
                        >
                          {tCommon("actions.edit")}
                        </Button>
                      )}
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
      </div>
      {nextCursor && (
        <div className="flex justify-end pt-3">
          <Button variant="outline" size="sm" onClick={goNext}>
            {tCommon("actions.nextPage")}
          </Button>
        </div>
      )}

      <ConfirmModal
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={tCommon("modal.confirmDelete")}
        message={
          deleteTarget
            ? tM("actions.deleteConfirm", { alias: deleteTarget.alias })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
