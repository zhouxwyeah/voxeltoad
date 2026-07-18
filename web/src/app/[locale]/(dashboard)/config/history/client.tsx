"use client";

import { useState, useTransition } from "react";
import { useRouter } from "@/i18n/navigation";
import { useTranslations } from "next-intl";
import { TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui";
import { ConfirmModal } from "@/components/modal";
import {
  rollbackAction,
  previewAction,
  diffAction,
  snapshotAction,
} from "./actions";

type Snapshot = { version: number; created_at?: string };

type PreviewResult = {
  valid: boolean;
  diff: Record<string, string[]>;
  impact: { new_resources?: number; deleted_resources?: number; changed_resources?: number };
  warnings: string[];
};

/**
 * Config snapshot history client. Lists snapshots, lets the operator inspect a
 * version, view a structured diff against the latest version, and roll back
 * with a two-step confirmation (design/domain-flows.md §rollback).
 */
export function ConfigHistoryPageClient({
  rows,
  nextCursor,
}: {
  rows: Snapshot[];
  nextCursor: string;
}) {
  const router = useRouter();
  const t = useTranslations("config-history");
  const [confirmVersion, setConfirmVersion] = useState<number | null>(null);
  const [pending, startTransition] = useTransition();

  function rollback() {
    if (confirmVersion === null) return;
    const v = confirmVersion;
    setConfirmVersion(null);
    startTransition(async () => {
      await rollbackAction(v);
    });
  }

  function loadMore() {
    const params = new URLSearchParams();
    if (nextCursor) params.set("cursor", nextCursor);
    router.push(`/config/history?${params.toString()}`);
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold text-foreground">
          {t("heading")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      <div className="overflow-hidden rounded-lg border border-border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-4 py-2 font-medium">{t("table.version")}</th>
              <th className="px-4 py-2 font-medium">{t("table.createdAt")}</th>
              <th className="px-4 py-2 text-right font-medium">
                {t("table.actions")}
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {rows.length === 0 && (
              <tr>
                <td
                  colSpan={3}
                  className="px-4 py-8 text-center text-muted-foreground"
                >
                  {t("table.empty")}
                </td>
              </tr>
            )}
            {rows.map((row) => (
              <SnapshotRow
                key={row.version}
                row={row}
                latestVersion={rows[0]?.version}
                disabled={pending}
                onRollback={() => setConfirmVersion(row.version)}
              />
            ))}
          </tbody>
        </table>
      </div>

      {nextCursor && (
        <div className="flex justify-center">
          <Button variant="outline" size="sm" onClick={loadMore} disabled={pending}>
            {t("actions.nextPage")}
          </Button>
        </div>
      )}

      {confirmVersion !== null && (
        <ConfirmModal
          open
          title={t("confirm.title")}
          message={`${t("confirm.body", { version: confirmVersion })} ${t("confirm.warning")}`}
          confirmLabel={t("confirm.confirm")}
          loadingLabel={t("confirm.rollingBack")}
          loading={pending}
          onCancel={() => setConfirmVersion(null)}
          onConfirm={rollback}
        />
      )}
    </div>
  );
}

function SnapshotRow({
  row,
  latestVersion,
  disabled,
  onRollback,
}: {
  row: Snapshot;
  latestVersion: number | undefined;
  disabled: boolean;
  onRollback: () => void;
}) {
  const t = useTranslations("config-history");
  const [diff, setDiff] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | {
        kind: "loaded";
        added: string[];
        removed: string[];
      }
    | { kind: "error"; message: string }
  >({ kind: "idle" });

  const [preview, setPreview] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "loaded"; result: PreviewResult }
    | { kind: "error"; message: string }
  >({ kind: "idle" });

  async function viewDiff() {
    if (latestVersion === undefined || latestVersion === row.version) {
      setDiff({ kind: "loaded", added: [], removed: [] });
      return;
    }
    setDiff({ kind: "loading" });
    try {
      const data = await diffAction(row.version, latestVersion);
      setDiff({
        kind: "loaded",
        added: [
          ...(data.added_providers ?? []),
          ...(data.added_models ?? []),
          ...(data.added_routes ?? []),
          ...(data.added_plugins ?? []),
        ],
        removed: [
          ...(data.deleted_providers ?? []),
          ...(data.deleted_models ?? []),
          ...(data.deleted_routes ?? []),
          ...(data.deleted_plugins ?? []),
        ],
      });
    } catch {
      setDiff({ kind: "error", message: t("diff.error") });
    }
  }

  async function viewPreview() {
    setPreview({ kind: "loading" });
    try {
      // Fetch the snapshot content for this version, then dry-run it.
      const snapshot = await snapshotAction(row.version);
      const result = await previewAction({
        providers: snapshot.providers ?? [],
        models: snapshot.models ?? [],
        routes: snapshot.routes ?? [],
        plugins: snapshot.plugins ?? [],
      });
      setPreview({ kind: "loaded", result });
    } catch {
      setPreview({ kind: "error", message: t("preview.error") });
    }
  }

  const isLatest = latestVersion === row.version;

  return (
    <>
      <tr className="hover:bg-muted/30">
        <td className="px-4 py-2 font-mono text-xs text-foreground">
          v{row.version}
          {isLatest && (
            <span className="ml-2 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
              {t("labels.current")}
            </span>
          )}
        </td>
        <td className="px-4 py-2 text-xs text-muted-foreground">
          {row.created_at
            ? new Date(row.created_at).toLocaleString()
            : "—"}
        </td>
        <td className="px-4 py-2 text-right">
          <button
            type="button"
            onClick={viewDiff}
            disabled={disabled}
            className="mr-2 rounded px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
          >
            {t("actions.diff")}
          </button>
          <button
            type="button"
            onClick={viewPreview}
            disabled={disabled}
            className="mr-2 rounded px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
          >
            {t("actions.preview")}
          </button>
          {!isLatest && (
            <button
              type="button"
              onClick={onRollback}
              disabled={disabled}
              className="rounded px-2 py-1 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
            >
              {t("actions.rollback")}
            </button>
          )}
        </td>
      </tr>
      {diff.kind !== "idle" && (
        <tr>
          <td colSpan={3} className="border-t border-border bg-muted/20 px-4 py-3">
            {diff.kind === "loading" && (
              <p className="text-xs text-muted-foreground">{t("diff.loading")}</p>
            )}
            {diff.kind === "error" && (
              <p className="text-xs text-destructive">{diff.message}</p>
            )}
            {diff.kind === "loaded" && (
              <DiffView added={diff.added} removed={diff.removed} />
            )}
          </td>
        </tr>
      )}
      {preview.kind !== "idle" && (
        <tr>
          <td colSpan={3} className="border-t border-border bg-muted/20 px-4 py-3">
            {preview.kind === "loading" && (
              <p className="text-xs text-muted-foreground">{t("preview.loading")}</p>
            )}
            {preview.kind === "error" && (
              <p className="text-xs text-destructive">{preview.message}</p>
            )}
            {preview.kind === "loaded" && (
              <PreviewView result={preview.result} />
            )}
          </td>
        </tr>
      )}
    </>
  );
}

function DiffView({ added, removed }: { added: string[]; removed: string[] }) {
  const t = useTranslations("config-history");
  return (
    <div className="flex flex-col gap-2">
      <div className="flex gap-4 text-xs">
        <span className="text-success">
          + {added.length} {t("diff.added")}
        </span>
        <span className="text-destructive">
          − {removed.length} {t("diff.removed")}
        </span>
      </div>
      {(added.length > 0 || removed.length > 0) && (
        <div className="flex flex-col gap-1 font-mono text-[11px]">
          {added.map((name) => (
            <span key={`a-${name}`} className="text-success">
              + {name}
            </span>
          ))}
          {removed.map((name) => (
            <span key={`r-${name}`} className="text-destructive">
              − {name}
            </span>
          ))}
        </div>
      )}
      {added.length === 0 && removed.length === 0 && (
        <p className="text-xs text-muted-foreground">{t("diff.noChanges")}</p>
      )}
    </div>
  );
}

function PreviewView({ result }: { result: PreviewResult }) {
  const t = useTranslations("config-history");
  const added = [
    ...(result.diff?.added_providers ?? []),
    ...(result.diff?.added_models ?? []),
    ...(result.diff?.added_routes ?? []),
    ...(result.diff?.added_plugins ?? []),
  ];
  const removed = [
    ...(result.diff?.removed_providers ?? []),
    ...(result.diff?.removed_models ?? []),
    ...(result.diff?.removed_routes ?? []),
    ...(result.diff?.removed_plugins ?? []),
  ];
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 text-xs">
        <span className={result.valid ? "text-success" : "text-destructive"}>
          {result.valid ? t("preview.valid") : t("preview.invalid")}
        </span>
        {result.impact && (
          <span className="text-muted-foreground">
            {t("preview.impact", {
              new: result.impact.new_resources ?? 0,
              deleted: result.impact.deleted_resources ?? 0,
            })}
          </span>
        )}
      </div>
      {result.warnings && result.warnings.length > 0 && (
        <div className="flex flex-col gap-1">
          {result.warnings.map((w, i) => (
            <p key={i} className="flex items-center gap-1.5 text-xs text-warning">
              <TriangleAlert className="h-3.5 w-3.5 shrink-0" />
              {w}
            </p>
          ))}
        </div>
      )}
      <div className="flex gap-4 text-xs">
        <span className="text-success">
          + {added.length} {t("diff.added")}
        </span>
        <span className="text-destructive">
          − {removed.length} {t("diff.removed")}
        </span>
      </div>
      {(added.length > 0 || removed.length > 0) && (
        <div className="flex flex-col gap-1 font-mono text-[11px]">
          {added.map((name) => (
            <span key={`a-${name}`} className="text-success">
              + {name}
            </span>
          ))}
          {removed.map((name) => (
            <span key={`r-${name}`} className="text-destructive">
              − {name}
            </span>
          ))}
        </div>
      )}
      {added.length === 0 && removed.length === 0 && (
        <p className="text-xs text-muted-foreground">{t("diff.noChanges")}</p>
      )}
    </div>
  );
}
