"use client";

import { useSearchParams } from "next/navigation";
import { useRouter } from "@/i18n/navigation";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import {
  localDatetimeToRfc3339,
  rfc3339ToLocalDatetime,
} from "@/lib/datetime";
import { RequestLogsTable } from "./table";

type RequestLogRow = Record<string, unknown>;

/**
 * Advanced search client. URL searchParams drive every filter so a query is
 * shareable and survives a refresh. The form writes back to the URL, which
 * re-runs the RSC fetch. Pagination state (page / page_size) also lives in
 * the URL; changing a filter resets to page 1.
 */
export function RequestLogsPageClient({
  rows,
  total,
  page,
  pageSize,
  isSuperAdmin,
}: {
  rows: RequestLogRow[];
  total: number;
  page: number;
  pageSize: number;
  isSuperAdmin: boolean;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const t = useTranslations("request-logs");

  const currentFrom = searchParams.get("from") ?? "";
  const currentTo = searchParams.get("to") ?? "";
  const currentTenant = searchParams.get("tenant") ?? "";
  const currentGroupName = searchParams.get("group_name") ?? "";
  const currentAPIKeyID = searchParams.get("api_key_id") ?? "";
  const currentProvider = searchParams.get("provider") ?? "";
  const currentModel = searchParams.get("model_requested") ?? "";
  const currentErrorType = searchParams.get("error_type") ?? "";
  const currentBlockedBy = searchParams.get("blocked_by") ?? "";
  const currentStream = searchParams.get("stream") ?? "";
  const currentFallback = searchParams.get("fallback") ?? "";
  const currentSessionID = searchParams.get("session_id") ?? "";
  const hasFilters =
    !!currentFrom ||
    !!currentTo ||
    !!currentTenant ||
    !!currentGroupName ||
    !!currentAPIKeyID ||
    !!currentProvider ||
    !!currentModel ||
    !!currentErrorType ||
    !!currentBlockedBy ||
    !!currentStream ||
    !!currentFallback ||
    !!currentSessionID;

  // Push a new URL keeping the current filters but overriding page and/or
  // page_size. Changing page keeps filters; changing a filter resets to 1.
  function pushParams(next: { page?: number; pageSize?: number }) {
    const params = new URLSearchParams(searchParams.toString());
    if (next.pageSize !== undefined) {
      params.set("page_size", String(next.pageSize));
      params.set("page", "1"); // new page size → restart at first page
    } else if (next.page !== undefined) {
      params.set("page", String(next.page));
    }
    router.push(`/request-logs?${params.toString()}`);
  }

  function applyFilter(formData: FormData) {
    const params = new URLSearchParams();
    // Preserve the current page size across a filter change; reset to page 1.
    params.set("page_size", String(pageSize));
    const set = (key: string, val: FormDataEntryValue | null) => {
      if (val) params.set(key, String(val));
    };
    set("from", formData.get("from") ? localDatetimeToRfc3339(formData.get("from") as string) : null);
    set("to", formData.get("to") ? localDatetimeToRfc3339(formData.get("to") as string) : null);
    set("tenant", formData.get("tenant"));
    set("group_name", formData.get("group_name"));
    set("api_key_id", formData.get("api_key_id"));
    set("provider", formData.get("provider"));
    set("model_requested", formData.get("model_requested"));
    set("error_type", formData.get("error_type"));
    set("blocked_by", formData.get("blocked_by"));
    set("stream", formData.get("stream"));
    set("fallback", formData.get("fallback"));
    set("session_id", formData.get("session_id"));
    router.push(`/request-logs?${params.toString()}`);
  }

  function resetFilter() {
    router.push("/request-logs");
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
        {isSuperAdmin && (
          <FilterField label={t("filters.tenant")}>
            <input
              name="tenant"
              type="text"
              defaultValue={currentTenant}
              placeholder="acme"
              className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
            />
          </FilterField>
        )}
        <FilterField label={t("filters.groupName")}>
          <input
            name="group_name"
            type="text"
            defaultValue={currentGroupName}
            placeholder="team-a"
            className="block h-8 w-28 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.apiKeyId")}>
          <input
            name="api_key_id"
            type="text"
            defaultValue={currentAPIKeyID}
            placeholder="key-..."
            className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.provider")}>
          <input
            name="provider"
            type="text"
            defaultValue={currentProvider}
            placeholder="openai"
            className="block h-8 w-28 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.model")}>
          <input
            name="model_requested"
            type="text"
            defaultValue={currentModel}
            placeholder="gpt-4o"
            className="block h-8 w-28 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.errorType")}>
          <input
            name="error_type"
            type="text"
            defaultValue={currentErrorType}
            placeholder="upstream_error"
            className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.blockedBy")}>
          <input
            name="blocked_by"
            type="text"
            defaultValue={currentBlockedBy}
            placeholder="ratelimit"
            className="block h-8 w-28 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
          />
        </FilterField>
        <FilterField label={t("filters.stream")}>
          <select
            name="stream"
            defaultValue={currentStream}
            className="block h-8 w-24 rounded border border-border bg-background px-2 text-xs text-foreground"
          >
            <option value="">{t("filters.any")}</option>
            <option value="true">{t("filters.streamTrue")}</option>
            <option value="false">{t("filters.streamFalse")}</option>
          </select>
        </FilterField>
        <FilterField label={t("filters.fallback")}>
          <select
            name="fallback"
            defaultValue={currentFallback}
            className="block h-8 w-24 rounded border border-border bg-background px-2 text-xs text-foreground"
          >
            <option value="">{t("filters.any")}</option>
            <option value="true">{t("filters.fallbackTrue")}</option>
            <option value="false">{t("filters.fallbackFalse")}</option>
          </select>
        </FilterField>
        <FilterField label={t("filters.sessionId")}>
          <input
            name="session_id"
            type="text"
            defaultValue={currentSessionID}
            placeholder="sess-..."
            className="block h-8 w-32 rounded border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground"
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

      <RequestLogsTable
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
