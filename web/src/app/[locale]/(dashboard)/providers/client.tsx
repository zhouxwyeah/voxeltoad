"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { ProviderForm } from "./create-form";
import { ProvidersTable } from "./providers-table";

type ProviderRow = Record<string, unknown>;

/**
 * Providers page client shell: toolbar + table + all Modals.
 * Stateful because Modal open/close + edit row selection need useState.
 */
export function ProvidersPageClient({
  rows,
  nextCursor,
}: {
  rows: ProviderRow[];
  nextCursor: string;
}) {
  const t = useTranslations("providers");
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<ProviderRow | null>(null);

  return (
    <>
      {/* Top action bar */}
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

      {/* Table */}
      <ProvidersTable
        rows={rows}
        nextCursor={nextCursor}
        onEdit={(row) => setEditRow(row)}
      />

      {/* Create Modal */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="md"
      >
        <ProviderForm
          defaultValues={null}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>

      {/* Edit Modal */}
      <Modal
        open={!!editRow}
        onClose={() => setEditRow(null)}
        title={t("modal.editTitle")}
        size="md"
      >
        <ProviderForm
          defaultValues={editRow}
          onSuccess={() => setEditRow(null)}
        />
      </Modal>
    </>
  );
}
