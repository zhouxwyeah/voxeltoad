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
import { setTenantEnabled } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";

type TenantRow = Record<string, unknown>;

/**
 * Tenants table + disable ConfirmModal (mirrors providers-table.tsx). Tenant
 * names are immutable, so there is no "edit" — the only mutation is the
 * reversible enabled/disabled toggle. Disabling is confirmed (it stops every
 * API key under the tenant); re-enabling is not (no destructive effect).
 */
export function TenantsTable({
  rows,
  nextCursor,
}: {
  rows: TenantRow[];
  nextCursor: string;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tT = useTranslations("tenants");

  // Disable ConfirmModal state.
  const [disableTarget, setDisableTarget] = useState<TenantRow | null>(null);
  const [disableLoading, setDisableLoading] = useState(false);
  const [disableError, setDisableError] = useState<string | null>(null);
  // Per-row enable-in-flight state (no confirmation needed for enabling).
  const [enablingName, setEnablingName] = useState<string | null>(null);

  const columns: ColumnDef<TenantRow>[] = useMemo(
    () => [
      { accessorKey: "id", header: tT("columns.id") },
      { accessorKey: "name", header: tT("columns.name") },
      {
        accessorKey: "enabled",
        header: tT("columns.status"),
        cell: ({ getValue }) => (
          <span
            className={
              getValue()
                ? "text-foreground"
                : "text-destructive"
            }
          >
            {getValue() ? tT("status.enabled") : tT("status.disabled")}
          </span>
        ),
      },
    ],
    [tT],
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
    const res = await setTenantEnabled(name, false);
    if (res.ok) {
      setDisableTarget(null);
      router.refresh();
    } else {
      setDisableError(res.error);
    }
    setDisableLoading(false);
  }

  async function enable(row: TenantRow) {
    const name = String(row.name ?? "");
    setEnablingName(name);
    await setTenantEnabled(name, true);
    setEnablingName(null);
    router.refresh();
  }

  function goNext() {
    const params = new URLSearchParams(searchParams.toString());
    params.set("cursor", nextCursor);
    router.push(`/tenants?${params.toString()}`);
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
                  {tT("actions.emptyState")}
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row) => {
                const enabled = Boolean(row.original.enabled);
                const name = String(row.original.name ?? "");
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
                      {enabled ? (
                        <Button
                          variant="destructive"
                          size="sm"
                          onClick={() => {
                            setDisableTarget(row.original);
                            setDisableError(null);
                          }}
                        >
                          {tT("actions.disable")}
                        </Button>
                      ) : (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={enablingName === name}
                          onClick={() => enable(row.original)}
                        >
                          {tT("actions.enable")}
                        </Button>
                      )}
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

      {/* Disable ConfirmModal */}
      <ConfirmModal
        open={!!disableTarget}
        onCancel={() => setDisableTarget(null)}
        onConfirm={confirmDisable}
        title={tT("modal.disableTitle")}
        message={
          disableTarget
            ? tT("actions.disableConfirm", {
                name: String(disableTarget.name ?? ""),
              })
            : ""
        }
        confirmLabel={tT("actions.disable")}
        loading={disableLoading}
        error={disableError}
      />
    </>
  );
}
