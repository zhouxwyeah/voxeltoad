"use client";

import { useTranslations } from "next-intl";
import { DataPlaneNodesTable } from "./table";

type NodeRow = Record<string, unknown>;

export function DataPlaneNodesPageClient({ rows }: { rows: NodeRow[] }) {
  const t = useTranslations("data-plane-nodes");

  return (
    <>
      <div>
        <h1 className="text-xl font-semibold text-foreground">
          {t("heading")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("subtitle")}
        </p>
      </div>
      <DataPlaneNodesTable rows={rows} />
    </>
  );
}
