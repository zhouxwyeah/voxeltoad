"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

/**
 * Plugin write actions (upsert + delete). Value validation is the admin API's
 * job (400 typed error); we don't re-check rules here. On success we
 * revalidate the list path so the RSC re-fetches.
 */
export async function upsertPlugin(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  if (!name) {
    const mapped = mapBackendError("plugin name is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const phaseRaw = String(formData.get("phase") ?? "").trim();
  const enabledRaw = formData.get("enabled");
  const scopeRaw = String(formData.get("scope") ?? "").trim();
  const paramsJson = String(formData.get("params_json") ?? "").trim();

  const body: Record<string, unknown> = { name };

  if (phaseRaw) {
    body.phase = phaseRaw;
  }
  if (enabledRaw === "true") {
    body.enabled = true;
  }
  if (scopeRaw) {
    body.scope = scopeRaw;
  }
  if (paramsJson) {
    try {
      body.params = JSON.parse(paramsJson);
    } catch {
      const mapped = mapBackendError("invalid params JSON");
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  }

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST("/api/v1/plugins", {
      // body is a superset-shaped PluginConfig; the required field is name.
      body: body as { name: string },
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "upsert failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/plugins");
  return { ok: true };
}

export async function updatePlugin(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  if (!name) {
    const mapped = mapBackendError("plugin name is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  // scope is identity (not patchable on the body); pass it as the query param.
  const scope = String(formData.get("scope") ?? "").trim();

  const phaseRaw = String(formData.get("phase") ?? "").trim();
  const enabledRaw = formData.get("enabled");
  const paramsJson = String(formData.get("params_json") ?? "").trim();

  const body: Record<string, unknown> = {};
  if (phaseRaw) {
    body.phase = phaseRaw;
  }
  body.enabled = enabledRaw === "true";
  if (paramsJson) {
    try {
      body.params = JSON.parse(paramsJson);
    } catch {
      const mapped = mapBackendError("invalid params JSON");
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  }

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.PATCH("/api/v1/plugins/{name}", {
      params: { path: { name }, query: { scope: scope || undefined } },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      body: body as any,
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "update failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/plugins");
  return { ok: true };
}

export async function deletePlugin(
  name: string,
  scope: string,
): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/plugins/{name}",
      { params: { path: { name }, query: { scope: scope || undefined } } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "delete failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/plugins");
  return { ok: true };
}
