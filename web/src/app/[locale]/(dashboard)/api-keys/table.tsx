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
import { revokeAPIKey } from "./actions";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";

type KeyRow = Record<string, unknown>;

export function APIKeysTable({
  rows,
  nextCursor,
  onEdit,
}: {
  rows: KeyRow[];
  nextCursor: string;
  onEdit?: (row: KeyRow) => void;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tCommon = useTranslations("common");
  const tK = useTranslations("api-keys");

  const [revokeTarget, setRevokeTarget] = useState<KeyRow | null>(null);
  const [revokeLoading, setRevokeLoading] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const columns: ColumnDef<KeyRow>[] = useMemo(
    () => [
      { accessorKey: "key_id", header: tK("columns.keyId") },
    ],
    [tK],
  );

  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  async function confirmRevoke() {
    if (!revokeTarget) return;
    setRevokeLoading(true);
    setRevokeError(null);
    const keyId = String(revokeTarget.key_id ?? "");
    const res = await revokeAPIKey(keyId);
    if (res.ok) {
      setRevokeTarget(null);
      router.refresh();
    } else {
      setRevokeError(res.error);
    }
    setRevokeLoading(false);
  }

  function goNext() {
    const params = new URLSearchParams(searchParams.toString());
    params.set("cursor", nextCursor);
    router.push(`/api-keys?${params.toString()}`);
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
                  {tK("actions.emptyState")}
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
                      className="px-4 py-2.5 text-foreground font-mono text-xs"
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
                          setRevokeTarget(row.original);
                          setRevokeError(null);
                        }}
                      >
                        {tK("actions.revoke")}
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
        open={!!revokeTarget}
        onCancel={() => setRevokeTarget(null)}
        onConfirm={confirmRevoke}
        title={tK("actions.revoke")}
        message={
          revokeTarget
            ? tK("modal.revokeConfirm", {
                keyId: String(revokeTarget.key_id ?? ""),
              })
            : ""
        }
        confirmLabel={tK("actions.revoke")}
        loading={revokeLoading}
        error={revokeError}
      />
    </>
  );
}
