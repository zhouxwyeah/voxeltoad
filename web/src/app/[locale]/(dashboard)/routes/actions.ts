"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

export async function upsertRoute(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const modelAlias = String(formData.get("model_alias") ?? "").trim();
  if (!modelAlias) {
    const mapped = mapBackendError("model alias is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const strategy = String(formData.get("strategy") ?? "").trim() || undefined;
  const providerNames = formData.getAll("route_provider_name").map(String);
  const providerWeights = formData.getAll("route_provider_weight").map(String);

  const providers = providerNames
    .map((name, i) => {
      const provider: { name: string; weight?: number } = { name };
      const weight = Number(providerWeights[i]);
      if (providerWeights[i] && !Number.isNaN(weight)) {
        provider.weight = weight;
      }
      return provider;
    })
    .filter((p) => p.name !== "");

  try {
    const client = await serverAdminClient();
    const body: Record<string, unknown> = { model_alias: modelAlias };
    if (providers.length > 0) body.providers = providers;
    if (strategy) body.strategy = strategy;

    const { error, response } = await client.POST("/api/v1/routes", {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      body: body as any,
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create/update failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/routes");
  return { ok: true };
}

export async function updateRoute(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const modelAlias = String(formData.get("model_alias") ?? "").trim();
  if (!modelAlias) {
    const mapped = mapBackendError("model alias is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const strategy = String(formData.get("strategy") ?? "").trim() || undefined;
  const providerNames = formData.getAll("route_provider_name").map(String);
  const providerWeights = formData.getAll("route_provider_weight").map(String);

  const providers = providerNames
    .map((name, i) => {
      const provider: { name: string; weight?: number } = { name };
      const weight = Number(providerWeights[i]);
      if (providerWeights[i] && !Number.isNaN(weight)) {
        provider.weight = weight;
      }
      return provider;
    })
    .filter((p) => p.name !== "");

  try {
    const client = await serverAdminClient();
    const body: Record<string, unknown> = {};
    body.providers = providers;
    if (strategy) body.strategy = strategy;

    const { error, response } = await client.PATCH("/api/v1/routes/{alias}", {
      params: { path: { alias: modelAlias } },
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
  revalidatePath("/routes");
  return { ok: true };
}

export async function deleteRoute(modelAlias: string): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/routes/{alias}",
      { params: { path: { alias: modelAlias } } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "delete failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/routes");
  return { ok: true };
}
