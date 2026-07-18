"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

/**
 * Operator write actions. Value validation is the admin API's job (400 typed
 * error); we don't re-check rules here. On success we revalidate the list path
 * so the RSC re-fetches.
 */
export async function createOperator(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const email = String(formData.get("email") ?? "").trim();
  const password = String(formData.get("password") ?? "").trim();
  const roleIdRaw = String(formData.get("role_id") ?? "").trim();
  const tenantId = String(formData.get("tenant_id") ?? "").trim();

  if (!email || !password) {
    const mapped = mapBackendError("email and password are required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  if (!roleIdRaw) {
    const mapped = mapBackendError("role is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const body: { email: string; password: string; role_id: number; tenant_id?: number } = {
    email,
    password,
    role_id: Number(roleIdRaw),
  };
  if (tenantId) body.tenant_id = Number(tenantId);

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST("/api/v1/operators", {
      body,
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/operators");
  return { ok: true };
}

export async function updateOperator(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const id = Number(formData.get("id"));
  const email = String(formData.get("email") ?? "").trim();
  const password = String(formData.get("password") ?? "").trim();
  const tenantId = String(formData.get("tenant_id") ?? "").trim();

  if (!id) {
    return { ok: false, error: "operator id is required" };
  }
  if (!email && !password && !tenantId) {
    const mapped = mapBackendError("at least one field must be provided");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const body: { email?: string; password?: string; role_id?: number; tenant_id?: number } = {};
  if (email) body.email = email;
  if (password) body.password = password;
  if (tenantId) body.tenant_id = Number(tenantId);

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.PUT("/api/v1/operators/{id}", {
      body,
      params: { path: { id } },
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "update failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/operators");
  return { ok: true };
}

export async function deleteOperator(id: number): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/operators/{id}",
      { params: { path: { id } } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "delete failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/operators");
  return { ok: true };
}
