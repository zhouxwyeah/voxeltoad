"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import type { FormResult } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

export async function createRole(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  const scopeKind = (String(formData.get("scope_kind") ?? "tenant")).trim() as "global" | "tenant";
  const description = String(formData.get("description") ?? "").trim();
  const permsStr = String(formData.get("permissions") ?? "").trim();
  const permissions = permsStr ? permsStr.split(",").filter(Boolean) : [];

  if (!name) {
    return { ok: false, error: "Name is required" };
  }

  try {
    const client = await serverAdminClient();
    unwrap(
      await client.POST("/api/v1/roles", {
        body: { name, scope_kind: scopeKind, description, permissions },
      }),
    );
    revalidatePath("/roles");
    return { ok: true };
  } catch (err) {
    const mapped = mapBackendError(String(err));
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
}

export async function updateRole(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const id = Number(formData.get("role_id"));
  const description = String(formData.get("description") ?? "").trim();
  const permsStr = String(formData.get("permissions") ?? "").trim();
  const permissions = permsStr ? permsStr.split(",").filter(Boolean) : [];

  if (!id) {
    return { ok: false, error: "Role ID is required" };
  }

  try {
    const client = await serverAdminClient();
    const body: Record<string, unknown> = { permissions };
    if (description) body.description = description;
    unwrap(await client.PATCH("/api/v1/roles/{id}", { params: { path: { id } }, body }));
    revalidatePath("/roles");
    return { ok: true };
  } catch (err) {
    const mapped = mapBackendError(String(err));
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
}

export async function deleteRole(id: number): Promise<FormResult | null> {
  try {
    const client = await serverAdminClient();
    unwrap(
      await client.DELETE("/api/v1/roles/{id}", {
        params: { path: { id } },
      }),
    );
    revalidatePath("/roles");
    return { ok: true };
  } catch (err) {
    const mapped = mapBackendError(String(err));
    return { ok: false, error: mapped.fallback };
  }
}
