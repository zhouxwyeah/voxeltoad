"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useState, useMemo } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { deletePlugin } from "./actions";
import { Button } from "@/components/ui";
import { Badge } from "@/components/ui/badge";
import { ConfirmModal, Modal } from "@/components/modal";

type PluginRow = {
  name: string;
  phase?: "pre" | "post";
  enabled?: boolean;
  scope?: string;
  params?: Record<string, unknown>;
};

/**
 * Plugins table + delete ConfirmModal + detail Modal.
 * Client component: owns table interaction (view/edit/delete) and
 * ConfirmModal/Detail Modal state.
 */
export function PluginsTable({
  rows,
  nextCursor,
  onEdit,
}: {
  rows: PluginRow[];
  nextCursor: string;
  onEdit?: (row: PluginRow) => void;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tP = useTranslations("plugins");

  // ConfirmModal state
  const [deleteTarget, setDeleteTarget] = useState<PluginRow | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // Detail Modal state
  const [detailRow, setDetailRow] = useState<PluginRow | null>(null);

  const columns: ColumnDef<PluginRow>[] = useMemo(
    () => [
      { accessorKey: "name", header: tP("columns.name") },
      {
        accessorKey: "phase",
        header: tP("columns.phase"),
        cell: ({ getValue }) => {
          const v = getValue<string | undefined>();
          if (!v) {
            return <span className="text-muted-foreground">—</span>;
          }
          return (
            <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-xs text-foreground">
              {v === "pre" ? tP("phase.pre") : tP("phase.post")}
            </span>
          );
        },
      },
      {
        accessorKey: "enabled",
        header: tP("columns.enabled"),
        cell: ({ getValue }) => {
          const v = getValue<boolean | undefined>();
          return v ? (
            <Badge variant="success">Yes</Badge>
          ) : (
            <Badge variant="secondary">No</Badge>
          );
        },
      },
      {
        accessorKey: "scope",
        header: tP("columns.scope"),
        cell: ({ getValue }) => {
          const v = getValue<string | undefined>();
          if (!v) {
            return <span className="text-muted-foreground">global</span>;
          }
          return <span>{v}</span>;
        },
      },
    ],
    [tP],
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
    const res = await deletePlugin(deleteTarget.name, deleteTarget.scope ?? "");
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
    router.push(`/plugins?${params.toString()}`);
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
                  {tP("actions.emptyState")}
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
                  <td className="px-4 py-2.5 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setDetailRow(row.original)}
                      >
                        {tP("actions.view")}
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

      {/* Delete ConfirmModal */}
      <ConfirmModal
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={tCommon("modal.confirmDelete")}
        message={
          deleteTarget
            ? tP("actions.deleteConfirm", { name: deleteTarget.name })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />

      {/* Detail Modal */}
      <Modal
        open={!!detailRow}
        onClose={() => setDetailRow(null)}
        title={tP("modal.detailTitle")}
        size="md"
      >
        {detailRow && (
          <div className="flex flex-col gap-3">
            <div>
              <span className="text-xs font-medium text-muted-foreground">
                {tP("columns.name")}
              </span>
              <p className="text-sm text-foreground">{detailRow.name}</p>
            </div>
            <div>
              <span className="text-xs font-medium text-muted-foreground">
                {tP("columns.phase")}
              </span>
              <p className="text-sm text-foreground">
                {detailRow.phase
                  ? detailRow.phase === "pre"
                    ? tP("phase.pre")
                    : tP("phase.post")
                  : "—"}
              </p>
            </div>
            <div>
              <span className="text-xs font-medium text-muted-foreground">
                {tP("columns.enabled")}
              </span>
              <p className="text-sm text-foreground">
                {detailRow.enabled ? "Yes" : "No"}
              </p>
            </div>
            <div>
              <span className="text-xs font-medium text-muted-foreground">
                {tP("columns.scope")}
              </span>
              <p className="text-sm text-foreground">
                {detailRow.scope || "global"}
              </p>
            </div>
            <div>
              <span className="text-xs font-medium text-muted-foreground">
                Params
              </span>
              <pre className="mt-1 rounded-md bg-muted p-3 text-xs font-mono overflow-auto">
                {JSON.stringify(detailRow.params ?? {}, null, 2)}
              </pre>
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
