"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { ModelForm } from "./create-form";
import { ModelsTable } from "./models-table";

type Pricing = {
  prompt_per_1m?: number;
  completion_per_1m?: number;
  currency?: string;
};

type ModelUpstream = {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing?: Pricing;
};

type ModelRow = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};
type ProviderOption = { name: string };

export function ModelsPageClient({
  rows,
  providers,
  nextCursor,
}: {
  rows: ModelRow[];
  providers: ProviderOption[];
  nextCursor: string;
}) {
  const t = useTranslations("models");
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<ModelRow | null>(null);
  const hasProviders = providers.length > 0;

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
        {hasProviders && (
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            {t("actions.create")}
          </Button>
        )}
      </div>

      {!hasProviders && (
        <div className="flex items-center justify-between rounded-md border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
          <span>{t("actions.noProviders")}</span>
          <Button href="/providers" variant="outline" size="sm">
            {t("actions.goToProviders")}
          </Button>
        </div>
      )}

      <ModelsTable
        rows={rows}
        nextCursor={nextCursor}
        onEdit={(row) => {
          setEditTarget(row);
          setEditOpen(true);
        }}
      />

      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="lg"
      >
        <ModelForm
          providers={providers}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>

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
          <ModelForm
            providers={providers}
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
