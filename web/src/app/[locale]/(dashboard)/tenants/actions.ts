"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

export async function createTenant(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  if (!name) {
    const mapped = mapBackendError("name is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST("/api/v1/tenants", {
      body: { name },
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/tenants");
  return { ok: true };
}

/**
 * setTenantEnabled toggles a tenant's enabled flag (reversible: unlike
 * api-key revocation, a disabled tenant can be re-enabled). Disabling rejects
 * every API key under the tenant at the data-plane auth boundary.
 */
export async function setTenantEnabled(
  name: string,
  enabled: boolean,
): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.PATCH(
      "/api/v1/tenants/{name}",
      { params: { path: { name } }, body: { enabled } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "update failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/tenants");
  return { ok: true };
}
