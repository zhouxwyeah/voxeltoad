"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

export async function createAPIKey(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult & { apiKey?: string }> {
  const keyId = String(formData.get("key_id") ?? "").trim();
  const allowedModels = formData.getAll("allowed_models").map(String).filter(Boolean);

  if (!keyId) {
    const mapped = mapBackendError("key_id is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  try {
    const client = await serverAdminClient();
    const body: { key_id: string; allowed_models?: string[] } = { key_id: keyId };
    if (allowedModels.length > 0) {
      body.allowed_models = allowedModels;
    }
    const { data, error, response } = await client.POST("/api/v1/api-keys", {
      body,
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
    revalidatePath("/api-keys");
    return { ok: true, apiKey: (data?.api_key as string) ?? "" };
  } catch (err) {
    return toFormError(err);
  }
}

export async function updateAPIKey(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const keyId = String(formData.get("key_id") ?? "").trim();
  const allowedModels = formData.getAll("allowed_models").map(String).filter(Boolean);

  if (!keyId) {
    return { ok: false, error: "key_id is required" };
  }
  if (allowedModels.length === 0) {
    const mapped = mapBackendError("allowed_models must be a non-empty array");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.PATCH(
      "/api/v1/api-keys/{key_id}",
      {
        body: { allowed_models: allowedModels },
        params: { path: { key_id: keyId } },
      },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "update failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/api-keys");
  return { ok: true };
}

export async function revokeAPIKey(keyId: string): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/api-keys/{key_id}",
      { params: { path: { key_id: keyId } } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "revoke failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/api-keys");
  return { ok: true };
}
