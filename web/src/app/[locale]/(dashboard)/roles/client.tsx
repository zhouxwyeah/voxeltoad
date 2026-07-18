"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { RoleForm } from "./create-form";
import type { RoleRow, PermissionItem } from "./page";

export function RolesPageClient({
  roles,
  permissions,
}: {
  roles: RoleRow[];
  permissions: PermissionItem[];
}) {
  const t = useTranslations("roles");
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<RoleRow | null>(null);

  return (
    <>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">
            {t("heading")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("subtitle")}
          </p>
        </div>
        <Button variant="primary" onClick={() => setCreateOpen(true)}>
          {t("actions.create")}
        </Button>
      </div>

      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-left text-sm">
          <thead className="border-b bg-muted/50 text-xs uppercase text-muted-foreground">
            <tr>
              <th className="px-4 py-3">{t("cols.name")}</th>
              <th className="px-4 py-3">{t("cols.scope")}</th>
              <th className="px-4 py-3">{t("cols.permissions_count")}</th>
              <th className="px-4 py-3">{t("cols.description")}</th>
              <th className="px-4 py-3 text-right">{t("cols.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {roles.map((r) => (
              <tr key={r.id} className="border-b last:border-0 hover:bg-muted/30">
                <td className="px-4 py-3 font-medium">
                  {r.name}
                  {r.is_builtin && (
                    <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                      {t("builtin")}
                    </span>
                  )}
                </td>
                <td className="px-4 py-3">
                  <span className={`rounded px-2 py-0.5 text-xs ${
                    r.scope_kind === "global"
                      ? "bg-blue-50 text-blue-700"
                      : "bg-emerald-50 text-emerald-700"
                  }`}>
                    {r.scope_kind === "global" ? t("scope.global") : t("scope.tenant")}
                  </span>
                </td>
                <td className="px-4 py-3 text-muted-foreground">
                  {r.permissions.length}
                  {r.permissions.includes("*") && ` (${t("wildcard")})`}
                </td>
                <td className="px-4 py-3 max-w-[200px] truncate text-muted-foreground">
                  {r.description || "—"}
                </td>
                <td className="px-4 py-3 text-right">
                  <button
                    className="rounded px-2 py-1 text-xs font-medium text-primary hover:bg-muted"
                    onClick={() => {
                      setEditTarget(r);
                      setEditOpen(true);
                    }}
                  >
                    {t("actions.edit")}
                  </button>
                  {!r.is_builtin && (
                    <DeleteButton role={r} />
                  )}
                </td>
              </tr>
            ))}
            {roles.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">
                  {t("empty")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Create modal */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="lg"
      >
        <RoleForm
          permissions={permissions}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>

      {/* Edit modal */}
      {editTarget && (
        <Modal
          open={editOpen}
          onClose={() => {
            setEditOpen(false);
            setEditTarget(null);
          }}
          title={t("modal.editTitle")}
          size="lg"
        >
          <RoleForm
            permissions={permissions}
            defaultValues={editTarget}
            onCancel={() => {
              setEditOpen(false);
              setEditTarget(null);
            }}
            onSuccess={() => {
              setEditOpen(false);
              setEditTarget(null);
            }}
          />
        </Modal>
      )}
    </>
  );
}

function DeleteButton({ role }: { role: RoleRow }) {
  const t = useTranslations("roles");
  const [confirming, setConfirming] = useState(false);

  if (confirming) {
    return (
      <span className="ml-2">
        <button
          className="rounded px-2 py-1 text-xs font-medium text-destructive hover:bg-destructive/10"
          onClick={async () => {
            const { deleteRole } = await import("./actions");
            const result = await deleteRole(role.id);
            if (result?.ok) {
              // refresh handled by router.refresh in form
              window.location.reload();
            } else {
              alert(result?.error ?? "Delete failed");
              setConfirming(false);
            }
          }}
        >
          {t("actions.confirm")}
        </button>
        <button
          className="ml-1 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
          onClick={() => setConfirming(false)}
        >
          {t("actions.cancel")}
        </button>
      </span>
    );
  }

  return (
    <button
      className="ml-2 rounded px-2 py-1 text-xs font-medium text-muted-foreground hover:text-destructive"
      onClick={() => setConfirming(true)}
    >
      {t("actions.delete")}
    </button>
  );
}
