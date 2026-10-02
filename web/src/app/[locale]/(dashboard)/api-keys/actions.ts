"use server";

import { revalidatePath } from "next/cache";
import { AdminError, unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

type CreateRequest = AdminPaths["/api/v1/api-keys"]["post"]["requestBody"]["content"]["application/json"];
type UpdateRequest = NonNullable<AdminPaths["/api/v1/api-keys/{key_id}"]["patch"]["requestBody"]>["content"]["application/json"];

async function formError(err: unknown): Promise<FormResult> {
  const result = await toFormError(err);
  if (!result.ok && err instanceof AdminError) {
    const mapped = mapBackendError(err.message);
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  return result;
}

export async function createAPIKey(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult & { apiKey?: string }> {
  try {
    const client = await serverAdminClient();
    const body: CreateRequest = {
      key_id: String(formData.get("key_id") ?? "").trim(),
      group_id: Number(formData.get("group_id")),
      application_id: Number(formData.get("application_id")),
      environment: String(formData.get("environment") ?? "") as CreateRequest["environment"],
    };
    const allowedModels = formData.getAll("allowed_models").map(String).filter(Boolean);
    if (allowedModels.length) body.allowed_models = allowedModels;
    const data = unwrap(await client.POST("/api/v1/api-keys", { body }));
    revalidatePath("/api-keys");
    return { ok: true, apiKey: data.api_key };
  } catch (err) {
    return formError(err);
  }
}

export async function updateAPIKey(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const body: UpdateRequest = {};
    const allowedModels = formData.getAll("allowed_models").map(String).filter(Boolean);
    if (allowedModels.length || formData.get("had_models") === "true" || formData.get("bind_identity") !== "true") body.allowed_models = allowedModels;
    if (formData.get("bind_identity") === "true") {
      body.group_id = Number(formData.get("group_id"));
      body.application_id = Number(formData.get("application_id"));
      body.environment = String(formData.get("environment") ?? "") as CreateRequest["environment"];
    }
    unwrap(await client.PATCH("/api/v1/api-keys/{key_id}", {
      body,
      params: { path: { key_id: String(formData.get("key_id") ?? "").trim() } },
    }));
    revalidatePath("/api-keys");
    return { ok: true };
  } catch (err) {
    return formError(err);
  }
}

export async function revokeAPIKey(keyId: string): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    unwrap(await client.DELETE("/api/v1/api-keys/{key_id}", { params: { path: { key_id: keyId } } }));
    revalidatePath("/api-keys");
    return { ok: true };
  } catch (err) {
    return formError(err);
  }
}
