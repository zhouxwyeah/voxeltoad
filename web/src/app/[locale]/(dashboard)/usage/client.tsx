"use client";

import { useState } from "react";
import { useSearchParams } from "next/navigation";
import { useRouter } from "@/i18n/navigation";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Select } from "@/components/ui/select";
import {
  localDatetimeToRfc3339,
  rfc3339ToLocalDatetime,
} from "@/lib/datetime";
import { UsageTable } from "./table";
import { UsageSummary } from "./summary";
import { UsageTimeseriesChart } from "./timeseries-chart";

const GROUP_BY_OPTIONS = [
  "tenant",
  "group_name",
  "api_key_id",
  "provider",
  "model",
  "application_id",
  "environment",
  "currency",
] as const;

export function UsagePageClient({
  rows,
  nextCursor,
  summaryRows,
  tenants,
  timeseriesRows,
  isSuperAdmin,
  groupBy,
}: {
  rows: Record<string, unknown>[];
  nextCursor: string;
  summaryRows: Record<string, unknown>[];
  tenants: Record<string, unknown>[];
  timeseriesRows: Record<string, unknown>[];
  isSuperAdmin: boolean;
  groupBy: string;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const t = useTranslations("usage");

  const currentFrom = searchParams.get("from") ?? "";
  const currentTo = searchParams.get("to") ?? "";
  const currentTenant = searchParams.get("tenant") ?? "";
  const currentProvider = searchParams.get("provider") ?? "";
  const currentModel = searchParams.get("model") ?? "";

  const currentApplication = searchParams.get("application_id") ?? "";
  const [environment, setEnvironment] = useState(searchParams.get("environment") ?? "");
  const [unattributed, setUnattributed] = useState(searchParams.get("unattributed") === "true");
  const [dimension, setDimension] = useState(groupBy);
  const [tenantValue, setTenantValue] = useState(currentTenant);
  const hasFilters =
    !!currentFrom ||
    !!currentTo ||
    !!currentTenant ||
    !!currentProvider ||
    !!currentModel || !!currentApplication || !!environment || unattributed;

  function applyFilter(formData: FormData) {
    const params = new URLSearchParams();
    const from = formData.get("from") as string;
    const to = formData.get("to") as string;
    const tenant = formData.get("tenant") as string;
    const provider = formData.get("provider") as string;
    const model = formData.get("model") as string;
    const groupBy = formData.get("group_by") as string;
    if (from) params.set("from", localDatetimeToRfc3339(from));
    if (to) params.set("to", localDatetimeToRfc3339(to));
    if (tenant) params.set("tenant", tenant);
    if (provider) params.set("provider", provider);
    if (model) params.set("model", model);
    if (groupBy) params.set("group_by", groupBy);
    if (environment) params.set("environment", environment);
    if (unattributed) params.set("unattributed", "true");
    else if (formData.get("application_id")) params.set("application_id", String(formData.get("application_id")));
    router.push(`/usage?${params.toString()}`);
  }

  function resetFilter() {
    router.push("/usage");
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      <UsageSummary rows={summaryRows} groupBy={groupBy || (isSuperAdmin ? "tenant" : "model")} />

      <UsageTimeseriesChart rows={timeseriesRows} />

      <form
        action={applyFilter}
        className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-muted/30 p-3"
      >
        <FilterField label={t("filters.from")}>
          <input
            name="from"
            type="datetime-local"
            defaultValue={rfc3339ToLocalDatetime(currentFrom)}
            className="block h-8 w-44 rounded border border-border bg-background px-2 text-xs text-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.to")}>
          <input
            name="to"
            type="datetime-local"
            defaultValue={rfc3339ToLocalDatetime(currentTo)}
            className="block h-8 w-44 rounded border border-border bg-background px-2 text-xs text-foreground"
          />
        </FilterField>
        {isSuperAdmin && (
          <FilterField label={t("filters.tenant")}>
            <Select
              name="tenant"
              value={tenantValue}
              onValueChange={setTenantValue}
              placeholder={t("filters.selectPlaceholder")}
              options={[
                { value: "", label: t("filters.selectPlaceholder") },
                ...tenants.map((tenant) => ({
                  value: String(tenant.name),
                  label: String(tenant.name),
                })),
              ]}
              className="w-40"
            />
          </FilterField>
        )}
        <FilterField label={t("filters.provider")}>
          <input
            name="provider"
            type="text"
            defaultValue={currentProvider}
            placeholder="openai"
            className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.model")}>
          <input
            name="model"
            type="text"
            defaultValue={currentModel}
            placeholder="gpt-4o"
            className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("identity.application")}>
          <input name="application_id" type="number" defaultValue={currentApplication} disabled={unattributed} className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground disabled:opacity-50" />
        </FilterField>
        <FilterField label={t("identity.environment")}>
          <Select name="environment" value={environment} onValueChange={setEnvironment} options={[{ value: "", label: t("identity.allEnvironments") }, ...["dev", "staging", "prod"].map((value) => ({ value, label: value }))]} className="w-32" />
        </FilterField>
        <label className="flex h-8 items-center gap-2 text-xs text-foreground">
          <input name="unattributed" type="checkbox" checked={unattributed} onChange={(event) => setUnattributed(event.target.checked)} />
          {t("identity.unattributedOnly")}
        </label>
        <FilterField label={t("filters.groupBy")}>
          <Select name="group_by" value={dimension} onValueChange={setDimension} options={GROUP_BY_OPTIONS.map((value) => ({ value, label: t(`filters.groupByOptions.${value}`) }))} className="w-32" />
        </FilterField>
        <div className="flex items-end gap-2">
          <Button type="submit" variant="primary" size="sm">
            {t("filters.apply")}
          </Button>
          {hasFilters && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={resetFilter}
            >
              {t("filters.reset")}
            </Button>
          )}
        </div>
      </form>

      <p className="text-xs text-muted-foreground">{t("identity.historyHint")}</p>
      <UsageTable rows={rows} nextCursor={nextCursor} />
    </div>
  );
}

function FilterField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-0.5">
      <span className="text-[11px] font-medium text-muted-foreground">
        {label}
      </span>
      {children}
    </label>
  );
}
