"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { PluginForm } from "./plugin-form";
import { PluginsTable } from "./plugins-table";

type PluginRow = {
  name: string;
  phase?: "pre" | "post";
  enabled?: boolean;
  scope?: string;
  params?: Record<string, unknown>;
};

/**
 * Plugins page client shell: toolbar + table + create/edit Modals.
 * Manages create/edit row local state; the PluginsTable owns its own
 * delete ConfirmModal and detail Modal state.
 */
export function PluginsPageClient({
  rows,
  nextCursor,
}: {
  rows: PluginRow[];
  nextCursor: string;
}) {
  const t = useTranslations("plugins");
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<PluginRow | null>(null);

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

      <PluginsTable
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
        <PluginForm
          defaultValues={null}
          onSuccess={() => setCreateOpen(false)}
          onCancel={() => setCreateOpen(false)}
        />
      </Modal>

      {/* Edit Modal */}
      <Modal
        open={!!editRow}
        onClose={() => setEditRow(null)}
        title={t("modal.editTitle")}
        size="md"
      >
        <PluginForm
          defaultValues={editRow}
          onSuccess={() => setEditRow(null)}
          onCancel={() => setEditRow(null)}
        />
      </Modal>
    </>
  );
}
