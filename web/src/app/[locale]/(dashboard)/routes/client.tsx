"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { RouteForm } from "./route-form";
import { RoutesTable } from "./routes-table";

type RouteProvider = {
  name: string;
  weight?: number;
};

type RouteRow = {
  model_alias: string;
  providers?: RouteProvider[];
  strategy?: "priority" | "weighted" | "round_robin" | "session_affinity";
};

type ModelOption = {
  alias: string;
  upstreams?: { provider: string; upstream_model: string }[];
};

type ProviderOption = { name: string };

export function RoutesPageClient({
  rows,
  nextCursor,
  models,
  providers,
}: {
  rows: RouteRow[];
  nextCursor: string;
  models: ModelOption[];
  providers: ProviderOption[];
}) {
  const t = useTranslations("routes");
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<RouteRow | null>(null);
  const [detailRow, setDetailRow] = useState<RouteRow | null>(null);
  const hasModels = models.length > 0;

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
        {hasModels && (
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            {t("actions.create")}
          </Button>
        )}
      </div>

      {!hasModels && (
        <div className="flex items-center justify-between rounded-md border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
          <span>{t("actions.noModels")}</span>
          <Button href="/models" variant="outline" size="sm">
            {t("actions.goToModels")}
          </Button>
        </div>
      )}

      <RoutesTable
        rows={rows}
        nextCursor={nextCursor}
        onView={(row) => setDetailRow(row)}
        onEdit={(row) => setEditRow(row)}
      />

      {/* Create Modal */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="lg"
      >
        <RouteForm
          models={models}
          providers={providers}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>

      {/* Edit Modal */}
      <Modal
        open={!!editRow}
        onClose={() => setEditRow(null)}
        title={t("modal.editTitle")}
        size="lg"
      >
        {editRow && (
          <RouteForm
            models={models}
            providers={providers}
            defaultValues={editRow}
            onCancel={() => setEditRow(null)}
            onSuccess={() => setEditRow(null)}
          />
        )}
      </Modal>

      {/* Detail Modal */}
      <Modal
        open={!!detailRow}
        onClose={() => setDetailRow(null)}
        title={t("modal.detailTitle")}
        size="lg"
      >
        {detailRow && (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {t("columns.modelAlias")}
              </span>
              <span className="text-sm text-foreground">
                {detailRow.model_alias}
              </span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {t("columns.strategy")}
              </span>
              <span className="text-sm text-foreground">
                {detailRow.strategy
                  ? t(`strategy.${detailRow.strategy}`)
                  : "-"}
              </span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {t("columns.providers")}
              </span>
              <div className="flex flex-col gap-1">
                {(detailRow.providers ?? []).length === 0 ? (
                  <span className="text-sm text-muted-foreground">
                    {t("actions.noProviders")}
                  </span>
                ) : (
                  (detailRow.providers ?? []).map((p, i) => (
                    <span key={i} className="text-sm text-foreground">
                      {p.name}
                      {p.weight !== undefined && ` · weight: ${p.weight}`}
                    </span>
                  ))
                )}
              </div>
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
