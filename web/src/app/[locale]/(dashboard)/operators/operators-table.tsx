"use client";

import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { deleteOperator } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";

type OperatorRow = {
  id: number;
  email: string;
  role: string;
  tenant_id?: number | null;
};

/**
 * Operators table + delete ConfirmModal.
 * Client component: owns delete ConfirmModal state. Data comes from the RSC
 * as props.
 *
 * The last super-admin can't be deleted (backend enforces 409); we hide the
 * delete button on that row as a frontend nicety, computed from the number
 * of super-admin rows visible in the current page.
 */
export function OperatorsTable({
  rows,
  onEdit,
}: {
  rows: OperatorRow[];
  onEdit?: (row: OperatorRow) => void;
}) {
  const router = useRouter();
  const tCommon = useTranslations("common");
  const tOp = useTranslations("operators");

  const [deleteTarget, setDeleteTarget] = useState<OperatorRow | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const superAdminCount = useMemo(
    () => rows.filter((r) => r.role === "super-admin").length,
    [rows],
  );

  const columns: ColumnDef<OperatorRow>[] = useMemo(
    () => [
      { accessorKey: "id", header: tOp("columns.id") },
      { accessorKey: "email", header: tOp("columns.email") },
      {
        accessorKey: "role",
        header: tOp("columns.role"),
        cell: ({ getValue }) => tOp(`roles.${getValue<string>()}`),
      },
      {
        accessorKey: "tenant_id",
        header: tOp("columns.tenantId"),
        cell: ({ getValue }) => {
          const v = getValue<number | null | undefined>();
          return v == null ? "—" : String(v);
        },
      },
    ],
    [tOp],
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
    const res = await deleteOperator(deleteTarget.id);
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
                  {tOp("actions.emptyState")}
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row) => {
                const isLastSuperAdmin =
                  row.original.role === "super-admin" && superAdminCount <= 1;
                return (
                  <tr
                    key={row.id}
                    className="border-b border-border last:border-b-0 transition-colors hover:bg-accent/50"
                  >
                    {row.getVisibleCells().map((cell) => (
                      <td
                        key={cell.id}
                        className="px-4 py-2.5 text-foreground"
                      >
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    ))}
                    <td className="px-4 py-2.5 text-right">
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
                        {!isLastSuperAdmin && (
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
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* Delete ConfirmModal */}
      <ConfirmModal
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={tCommon("modal.confirmDelete")}
        message={
          deleteTarget
            ? tOp("actions.deleteConfirm", { email: deleteTarget.email })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
