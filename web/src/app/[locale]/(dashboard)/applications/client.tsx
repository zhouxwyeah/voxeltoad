"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { ApplicationForm } from "./form";
import { ApplicationsTable } from "./table";

type Row = Record<string, unknown>;

export function ApplicationsPageClient({
  rows,
  nextCursor,
  groups,
}: {
  rows: Row[];
  nextCursor: string;
  groups: Row[];
}) {
  const t = useTranslations("applications");
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
      <ApplicationsTable rows={rows} nextCursor={nextCursor} />
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="sm"
      >
        <ApplicationForm
          groups={groups}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>
    </>
  );
}
