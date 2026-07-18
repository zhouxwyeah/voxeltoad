"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { TenantForm } from "./form";
import { TenantsTable } from "./table";

type TenantRow = Record<string, unknown>;

/**
 * Tenants page client shell: toolbar + table (create only — tenant names are
 * immutable, so there is no "edit" modal; disable/enable lives in the table).
 */
export function TenantsPageClient({
  rows,
  nextCursor,
}: {
  rows: TenantRow[];
  nextCursor: string;
}) {
  const t = useTranslations("tenants");
  const [createOpen, setCreateOpen] = useState(false);

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
      <TenantsTable rows={rows} nextCursor={nextCursor} />
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="sm"
      >
        <TenantForm
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>
    </>
  );
}
