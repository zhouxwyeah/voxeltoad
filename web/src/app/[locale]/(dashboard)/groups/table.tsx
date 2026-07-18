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
import { setGroupEnabled, deleteGroup } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";

type GroupRow = Record<string, unknown>;

export function GroupsTable({
  rows,
  nextCursor,
}: {
  rows: GroupRow[];
  nextCursor: string;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tG = useTranslations("groups");

  const [disableTarget, setDisableTarget] = useState<GroupRow | null>(null);
  const [disableLoading, setDisableLoading] = useState(false);
  const [disableError, setDisableError] = useState<string | null>(null);
  const [enablingName, setEnablingName] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<GroupRow | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const columns: ColumnDef<GroupRow>[] = useMemo(
    () => [
      { accessorKey: "id", header: tG("columns.id") },
      { accessorKey: "name", header: tG("columns.name") },
      {
        accessorKey: "enabled",
        header: tG("columns.status"),
        cell: ({ getValue }) => (
          <span className={getValue() ? "text-foreground" : "text-destructive"}>
            {getValue() ? tG("status.enabled") : tG("status.disabled")}
          </span>
        ),
      },
    ],
    [tG],
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
    const res = await setGroupEnabled(name, false);
    if (res.ok) {
      setDisableTarget(null);
      router.refresh();
    } else {
      setDisableError(res.error);
    }
    setDisableLoading(false);
  }

  async function enable(row: GroupRow) {
    const name = String(row.name ?? "");
    setEnablingName(name);
    await setGroupEnabled(name, true);
    setEnablingName(null);
    router.refresh();
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setDeleteError(null);
    const name = String(deleteTarget.name ?? "");
    const res = await deleteGroup(name);
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
    router.push(`/groups?${params.toString()}`);
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
                  {tG("actions.emptyState")}
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
                            {tG("actions.disable")}
                          </Button>
                        ) : (
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={enablingName === name}
                            onClick={() => enable(row.original)}
                          >
                            {tG("actions.enable")}
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
        title={tG("modal.disableTitle")}
        message={
          disableTarget
            ? tG("actions.disableConfirm", { name: String(disableTarget.name ?? "") })
            : ""
        }
        confirmLabel={tG("actions.disable")}
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
            ? tG("actions.deleteConfirm", { name: String(deleteTarget.name ?? "") })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
