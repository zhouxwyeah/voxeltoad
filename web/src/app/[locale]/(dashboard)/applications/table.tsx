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
import { setApplicationEnabled, deleteApplication } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";

type Row = Record<string, unknown>;

export function ApplicationsTable({
  rows,
  nextCursor,
}: {
  rows: Row[];
  nextCursor: string;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tA = useTranslations("applications");

  const [disableTarget, setDisableTarget] = useState<Row | null>(null);
  const [disableLoading, setDisableLoading] = useState(false);
  const [disableError, setDisableError] = useState<string | null>(null);
  const [enablingName, setEnablingName] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<Row | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const columns: ColumnDef<Row>[] = useMemo(
    () => [
      { accessorKey: "id", header: tA("columns.id") },
      { accessorKey: "name", header: tA("columns.name") },
      { accessorKey: "owner_group_name", header: tA("columns.ownerGroup") },
      {
        accessorKey: "enabled",
        header: tA("columns.status"),
        cell: ({ getValue }) => (
          <span className={getValue() ? "text-foreground" : "text-destructive"}>
            {getValue() ? tA("status.enabled") : tA("status.disabled")}
          </span>
        ),
      },
    ],
    [tA],
  );

  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  async function confirmDisable() {
    if (!disableTarget) return;
    setDisableLoading(true);
    setDisableError(null);
    const name = String(disableTarget.name ?? "");
    const res = await setApplicationEnabled(name, false);
    if (res.ok) {
      setDisableTarget(null);
      router.refresh();
    } else {
      setDisableError(res.error);
    }
    setDisableLoading(false);
  }

  async function enable(row: Row) {
    const name = String(row.name ?? "");
    setEnablingName(name);
    await setApplicationEnabled(name, true);
    setEnablingName(null);
    router.refresh();
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setDeleteError(null);
    const name = String(deleteTarget.name ?? "");
    const res = await deleteApplication(name);
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
    router.push(`/applications?${params.toString()}`);
  }

  return (
    <>
      <div className="overflow-hidden rounded-lg border border-border bg-background">
        <table className="w-full border-collapse text-sm">
          <thead>
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id} className="border-b border-border bg-muted text-left">
                {hg.headers.map((h) => (
                  <th key={h.id} className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
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
                <td colSpan={columns.length + 1} className="px-4 py-10 text-center text-muted-foreground">
                  {tA("actions.emptyState")}
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row) => {
                const enabled = Boolean(row.original.enabled);
                const name = String(row.original.name ?? "");
                return (
                  <tr key={row.id} className="border-b border-border last:border-b-0 transition-colors hover:bg-accent/50">
                    {row.getVisibleCells().map((cell) => (
                      <td key={cell.id} className="px-4 py-2.5 text-foreground">
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    ))}
                    <td className="px-4 py-2.5 text-right">
                      <div className="flex items-center justify-end gap-1">
                        {enabled ? (
                          <Button
                            variant="destructive"
                            size="sm"
                            onClick={() => {
                              setDisableTarget(row.original);
                              setDisableError(null);
                            }}
                          >
                            {tA("actions.disable")}
                          </Button>
                        ) : (
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={enablingName === name}
                            onClick={() => enable(row.original)}
                          >
                            {tA("actions.enable")}
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
                );
              })
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
        open={!!disableTarget}
        onCancel={() => setDisableTarget(null)}
        onConfirm={confirmDisable}
        title={tA("modal.disableTitle")}
        message={
          disableTarget
            ? tA("actions.disableConfirm", { name: String(disableTarget.name ?? "") })
            : ""
        }
        confirmLabel={tA("actions.disable")}
        loading={disableLoading}
        error={disableError}
      />

      <ConfirmModal
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={tCommon("modal.confirmDelete")}
        message={
          deleteTarget
            ? tA("actions.deleteConfirm", { name: String(deleteTarget.name ?? "") })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
