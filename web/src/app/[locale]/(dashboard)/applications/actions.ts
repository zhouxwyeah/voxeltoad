"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

export async function createApplication(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  const ownerGroup = String(formData.get("owner_group") ?? "").trim();
  if (!name) {
    const mapped = mapBackendError("name is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  if (!ownerGroup) {
    const mapped = mapBackendError("owner group is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST("/api/v1/applications", {
      body: { name, owner_group: ownerGroup },
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/applications");
  return { ok: true };
}

export async function setApplicationEnabled(
  name: string,
  enabled: boolean,
): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.PATCH(
      "/api/v1/applications/{name}",
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
  revalidatePath("/applications");
  return { ok: true };
}

export async function deleteApplication(name: string): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/applications/{name}",
      { params: { path: { name } } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "delete failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/applications");
  return { ok: true };
}
