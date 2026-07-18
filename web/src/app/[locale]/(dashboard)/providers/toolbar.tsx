"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { ProviderForm } from "./create-form";

/**
 * Providers toolbar: "Create provider" button + create/edit Modals.
 * Client component because Modal state needs useState.
 */
export function ProvidersToolbar() {
  const t = useTranslations("providers");
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<Record<string, unknown> | null>(null);

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
