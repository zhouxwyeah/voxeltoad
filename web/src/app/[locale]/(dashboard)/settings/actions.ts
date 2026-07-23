"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

/**
 * Gateway settings write action. PUTs the whole settings document; the admin
 * API validates and bumps config_generation so the data plane picks up the
 * change on its next poll (hot-reloadable).
 */
export async function updateSettings(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const captureEnabled = formData.get("capture_enabled") === "true";
  const maxBodyKB = Number(formData.get("max_body_kb") ?? 0) || 0;
  const retentionDays = Number(formData.get("retention_days") ?? 7) || 0;
  const anthropicDisabled = formData.get("anthropic_disabled") === "true";

  const body = {
    trace: {
      capture_payload_enabled: captureEnabled,
      max_body_kb: maxBodyKB,
      retention_days: retentionDays,
    },
    ingress: {
      anthropic_disabled: anthropicDisabled,
    },
  };

  try {
    const client = await serverAdminClient();
    const { error } = await client.PUT("/api/v1/gateway-settings", { body });
    if (error) {
      const message = error.error?.message ?? "save failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/settings");
  return { ok: true };
}
