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
import { deleteProvider, testProvider } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";
import { toast } from "@/lib/toast";

type ProviderRow = Record<string, unknown>;

/**
 * Providers table + delete ConfirmModal (design-system.md §9).
 * Client component: owns table interaction (edit/delete) and ConfirmModal state.
 * Data comes from the RSC as props; "next page" pushes ?cursor=.
 */
export function ProvidersTable({
  rows,
  nextCursor,
  onEdit,
}: {
  rows: ProviderRow[];
  nextCursor: string;
  onEdit?: (row: ProviderRow) => void;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tP = useTranslations("providers");
  const tErr = useTranslations("errors");

  // ConfirmModal state
  const [deleteTarget, setDeleteTarget] = useState<ProviderRow | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // Connectivity test state: name of the row currently being probed.
  const [testingName, setTestingName] = useState<string | null>(null);

  async function runTest(name: string) {
    setTestingName(name);
    const res = await testProvider(name);
    if (res.ok) {
      toast.success(tP("test.success", { latency: res.latencyMs }));
    } else {
      toast.error(
        tP("test.failed", {
          error: res.errorKey ? tErr(res.errorKey) : res.error,
        }),
      );
    }
    setTestingName(null);
  }

  const columns: ColumnDef<ProviderRow>[] = useMemo(
    () => [
      { accessorKey: "name", header: tP("columns.name") },
      { accessorKey: "type", header: tP("columns.type") },
      {
        id: "endpoints",
        header: tP("columns.adapter"),
        cell: ({ row }) => {
          const eps = row.original.endpoints as Array<{ adapter?: string }> | undefined;
          if (!eps || eps.length === 0)
            return <span className="text-muted-foreground">—</span>;
          return (
            <div className="flex flex-wrap gap-1">
              {eps.map((ep, i) => {
                const v = ep.adapter ?? "";
                const label = v === "claude" ? "Anthropic" : v === "openai" ? "OpenAI" : v;
                const color =
                  v === "claude"
                    ? "bg-orange-500/10 text-orange-600 dark:text-orange-400"
                    : "bg-blue-500/10 text-blue-600 dark:text-blue-400";
                return (
                  <span key={i} className={`rounded-full px-2 py-0.5 text-xs font-medium ${color}`}>
                    {label}
                  </span>
                );
              })}
            </div>
          );
        },
      },
      { accessorKey: "base_url", header: tP("columns.baseUrl") },
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
    const name = String(deleteTarget.name ?? "");
    const res = await deleteProvider(name);
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
    router.push(`/providers?${params.toString()}`);
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
                      {String(cell.getValue() ?? "")}
                    </td>
                  ))}
                  <td className="px-4 py-2.5 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={testingName === String(row.original.name ?? "")}
                        onClick={() => runTest(String(row.original.name ?? ""))}
                      >
                        {testingName === String(row.original.name ?? "")
                          ? tP("test.testing")
                          : tP("test.actionShort")}
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
        message={deleteTarget
          ? tP("actions.deleteConfirm", { name: String(deleteTarget.name ?? "") })
          : ""}
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
