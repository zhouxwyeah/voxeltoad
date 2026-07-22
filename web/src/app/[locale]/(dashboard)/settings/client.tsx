"use client";

import { useActionState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { updateSettings } from "./actions";
import type { FormResult } from "@/lib/errors";

type GatewaySettings = {
  trace?: {
    capture_payload_enabled?: boolean;
    max_body_kb?: number;
    retention_days?: number;
  };
  ingress?: {
    anthropic_disabled?: boolean;
  };
};

/**
 * Settings client: a grouped form for the hot-reloadable gateway behavior
 * parameters. Each field is annotated Hot (applies within one poll) so
 * operators know what to expect.
 */
export function SettingsClient({ initial }: { initial: GatewaySettings }) {
  const t = useTranslations("settings");
  const [result, action, pending] = useActionState(updateSettings, null as FormResult | null);

  const trace = initial.trace ?? {};
  const ingress = initial.ingress ?? {};

  return (
    <>
      <div className="flex flex-col gap-2">
        <h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1>
        <p className="text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      <form action={action} className="flex flex-col gap-6">
        {/* Trace capture section */}
        <section className="flex flex-col gap-4 rounded-lg border border-border p-5">
          <div className="flex flex-col gap-1">
            <h2 className="text-sm font-semibold text-foreground">
              {t("trace.title")}
            </h2>
            <p className="text-xs text-muted-foreground">
              {t("trace.description")}
            </p>
          </div>

          {/* Enable capture (checkbox) */}
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              name="capture_enabled"
              value="true"
              defaultChecked={!!trace.capture_payload_enabled}
              className="mt-1 h-4 w-4 rounded border-border"
            />
            <span className="flex flex-col">
              <span className="flex items-center gap-2 text-sm font-medium text-foreground">
                {t("trace.captureEnabled.label")}
                <HotBadge label={t("trace.captureEnabled.hot")} />
              </span>
              <span className="text-xs text-muted-foreground">
                {t("trace.captureEnabled.help")}
              </span>
            </span>
          </label>

          {/* Max body KB */}
          <label className="flex flex-col gap-1">
            <span className="flex items-center gap-2 text-sm font-medium text-foreground">
              {t("trace.maxBodyKB.label")}
              <HotBadge label={t("trace.maxBodyKB.hot")} />
            </span>
            <input
              type="number"
              name="max_body_kb"
              min={0}
              defaultValue={trace.max_body_kb ?? 0}
              className="mt-1 w-40 rounded-md border border-border bg-background px-3 py-1.5 text-sm"
            />
            <span className="text-xs text-muted-foreground">
              {t("trace.maxBodyKB.help")}
            </span>
          </label>

          {/* Retention days */}
          <label className="flex flex-col gap-1">
            <span className="flex items-center gap-2 text-sm font-medium text-foreground">
              {t("trace.retentionDays.label")}
              <HotBadge label={t("trace.retentionDays.hot")} />
            </span>
            <input
              type="number"
              name="retention_days"
              min={0}
              defaultValue={trace.retention_days ?? 7}
              className="mt-1 w-40 rounded-md border border-border bg-background px-3 py-1.5 text-sm"
            />
            <span className="text-xs text-muted-foreground">
              {t("trace.retentionDays.help")}
            </span>
          </label>
        </section>

        {/* Ingress protocol section */}
        <section className="flex flex-col gap-4 rounded-lg border border-border p-5">
          <div className="flex flex-col gap-1">
            <h2 className="text-sm font-semibold text-foreground">
              {t("ingress.title")}
            </h2>
            <p className="text-xs text-muted-foreground">
              {t("ingress.description")}
            </p>
          </div>

          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              name="anthropic_disabled"
              value="true"
              defaultChecked={!!ingress.anthropic_disabled}
              className="mt-1 h-4 w-4 rounded border-border"
            />
            <span className="flex flex-col">
              <span className="flex items-center gap-2 text-sm font-medium text-foreground">
                {t("ingress.anthropicDisabled.label")}
                <HotBadge label={t("ingress.anthropicDisabled.hot")} />
              </span>
              <span className="text-xs text-muted-foreground">
                {t("ingress.anthropicDisabled.help")}
              </span>
            </span>
          </label>
        </section>

        <div className="flex items-center gap-3">
          <Button type="submit" disabled={pending}>
            {pending ? t("saving") : t("save")}
          </Button>
          {result?.ok && (
            <span className="text-sm text-success">
              {t("saved")}
            </span>
          )}
          {result && !result.ok && (
            <span className="text-sm text-destructive">{result.error}</span>
          )}
        </div>
      </form>
    </>
  );
}

function HotBadge({ label }: { label: string }) {
  return (
    <span className="rounded-full bg-success/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-success">
      {label}
    </span>
  );
}
