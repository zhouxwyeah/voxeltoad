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
import { AuditTable } from "./table";

export function AuditPageClient({
  rows,
  total,
  page,
  pageSize,
}: {
  rows: Record<string, unknown>[];
  total: number;
  page: number;
  pageSize: number;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const t = useTranslations("audit");

  const currentFrom = searchParams.get("from") ?? "";
  const currentTo = searchParams.get("to") ?? "";
  const currentResourceType = searchParams.get("resource_type") ?? "";
  const currentAction = searchParams.get("action") ?? "";
  const hasFilters =
    !!currentFrom || !!currentTo || !!currentResourceType || !!currentAction;

  const [actionValue, setActionValue] = useState(currentAction);

  // Push a new URL keeping filters but overriding page and/or page_size.
  function pushParams(next: { page?: number; pageSize?: number }) {
    const params = new URLSearchParams(searchParams.toString());
    if (next.pageSize !== undefined) {
      params.set("page_size", String(next.pageSize));
      params.set("page", "1"); // new page size → restart at first page
    } else if (next.page !== undefined) {
      params.set("page", String(next.page));
    }
    router.push(`/audit?${params.toString()}`);
  }

  function applyFilter(formData: FormData) {
    const params = new URLSearchParams();
    // Preserve page size; a filter change restarts at page 1.
    params.set("page_size", String(pageSize));
    const from = formData.get("from") as string;
    const to = formData.get("to") as string;
    const resourceType = formData.get("resource_type") as string;
    const action = formData.get("action") as string;
    if (from) params.set("from", localDatetimeToRfc3339(from));
    if (to) params.set("to", localDatetimeToRfc3339(to));
    if (resourceType) params.set("resource_type", resourceType);
    if (action) params.set("action", action);
    router.push(`/audit?${params.toString()}`);
  }

  function resetFilter() {
    router.push("/audit");
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

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
        <FilterField label={t("filters.resourceType")}>
          <input
            name="resource_type"
            type="text"
            defaultValue={currentResourceType}
            placeholder="providers"
            className="block h-8 w-40 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.action")}>
          <Select
            name="action"
            value={actionValue}
            onValueChange={setActionValue}
            placeholder={t("filters.all")}
            options={[
              { value: "", label: t("filters.all") },
              { value: "create", label: t("actions.create") },
              { value: "update", label: t("actions.update") },
              { value: "delete", label: t("actions.delete") },
            ]}
            className="w-32"
          />
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

      <AuditTable
        rows={rows}
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={(p) => pushParams({ page: p })}
        onPageSizeChange={(s) => pushParams({ pageSize: s })}
      />
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


