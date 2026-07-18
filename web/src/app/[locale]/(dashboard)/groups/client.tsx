"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { GroupForm } from "./form";
import { GroupsTable } from "./table";

type GroupRow = Record<string, unknown>;

export function GroupsPageClient({
  rows,
  nextCursor,
}: {
  rows: GroupRow[];
  nextCursor: string;
}) {
  const t = useTranslations("groups");
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
      <GroupsTable rows={rows} nextCursor={nextCursor} />
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="sm"
      >
        <GroupForm
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>
    </>
  );
}
