"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

export async function topupQuota(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const scope = String(formData.get("scope") ?? "").trim();
  const amount = Number(formData.get("amount"));
  const currency = String(formData.get("currency") ?? "").trim() || "USD";

  if (!scope) {
    const mapped = mapBackendError("scope is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  if (!amount || amount <= 0) {
    const mapped = mapBackendError("amount must be positive");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  // Convert display amount to micro-units (1 display unit = 1,000,000 micro).
  const delta = amount * 1_000_000;

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST("/api/v1/quotas/topup", {
      body: { scope, delta, currency },
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "top-up failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/quotas");
  return { ok: true };
}
