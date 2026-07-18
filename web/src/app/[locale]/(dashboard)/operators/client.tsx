"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { OperatorForm } from "./create-form";
import { OperatorsTable } from "./operators-table";

type OperatorRow = {
  id: number;
  email: string;
  role: string;
  tenant_id?: number | null;
};

type TenantOption = { id: number; name: string };
type RoleOption = { id: number; name: string; scope_kind: string };

export function OperatorsPageClient({
  rows,
  tenants,
  roles,
}: {
  rows: OperatorRow[];
  tenants: TenantOption[];
  roles: RoleOption[];
}) {
  const t = useTranslations("operators");
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<OperatorRow | null>(null);

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

      <OperatorsTable
        rows={rows}
        onEdit={(row) => {
          setEditTarget(row);
          setEditOpen(true);
        }}
      />

      {/* Create modal */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="md"
      >
        <OperatorForm
          tenants={tenants}
          roles={roles}
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
          size="md"
        >
          <OperatorForm
            tenants={tenants}
            roles={roles}
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
