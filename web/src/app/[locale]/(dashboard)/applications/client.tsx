"use client";

import { useState } from "react";
import { useFormatter, useTranslations } from "next-intl";
import type { AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { Button, Card, DetailField } from "@/components/ui";
import { microToDisplay } from "@/lib/money";
import { Modal } from "@/components/modal";
import { ApplicationForm } from "./form";
import { ApplicationsTable } from "./table";

type Row = Record<string, unknown>;

export function ApplicationsPageClient({
  rows,
  nextCursor,
  groups,
  attribution,
  attributionError,
}: {
  rows: Row[];
  nextCursor: string;
  groups: Row[];
  attribution: AdminPaths["/api/v1/usage/attribution"]["get"]["responses"][200]["content"]["application/json"] | null;
  attributionError: "error" | "forbidden" | null;
}) {
  const t = useTranslations("applications");
  const format = useFormatter();
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
      <Card className="flex flex-col gap-3 p-4">
        <p className="text-sm text-muted-foreground">{t("migration.description")}</p>
        <Button href="/api-keys?unbound=true" variant="outline" size="sm">{t("migration.unboundKeys")}</Button>
        <p className="text-sm text-muted-foreground">{t("migration.disableHint")}</p>
      </Card>
      {(attribution || attributionError) && (
        <Card className="flex flex-col gap-4 p-4">
          <h2 className="text-sm font-semibold text-foreground">{t("attribution.heading")}</h2>
          {attributionError ? (
            <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {t(`attribution.${attributionError}`)}
            </p>
          ) : attribution && (
            <>
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <DetailField label={t("attribution.unboundKeys")}>{format.number(attribution.unbound_key_count)}</DetailField>
                <DetailField label={t("attribution.requestCount")}>{format.number(attribution.request_count)}</DetailField>
                <DetailField label={t("attribution.unattributedRequests")}>{format.number(attribution.unattributed_request_count)}</DetailField>
                <DetailField label={t("attribution.unattributedRatio")}>
                  {format.number(attribution.unattributed_request_ratio, { style: "percent", maximumFractionDigits: 2 })}
                </DetailField>
              </div>
              <DetailField label={t("attribution.recordedCosts")}>
                {attribution.recorded_unattributed_costs.length > 0 ? (
                  <ul className="flex flex-wrap gap-4">
                    {attribution.recorded_unattributed_costs.map(({ currency, cost }) => (
                      <li key={currency}>{microToDisplay(cost)} {currency || t("attribution.unknownCurrency")}</li>
                    ))}
                  </ul>
                ) : t("attribution.noCosts")}
              </DetailField>
              <p className="text-xs text-muted-foreground">{t("attribution.keyHint")}</p>
              <p className="text-xs text-muted-foreground">{t("attribution.historyHint")}</p>
            </>
          )}
        </Card>
      )}
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
