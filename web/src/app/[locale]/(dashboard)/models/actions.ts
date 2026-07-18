"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";
import { displayToMicro } from "@/lib/money";

type ModelUpstream = {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing?: {
    prompt_per_1m: number;
    completion_per_1m: number;
    currency: string;
    cache_hit_multiplier?: number;
  };
};

/**
 * Parse a comma/space-separated tag string into a clean list. Handles the
 * common forms operators type: "vision, function_calling" or "a b,c".
 */
function parseList(raw: string): string[] {
  return raw
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/**
 * Model write actions. Value validation (alias uniqueness, upstream provider
 * existence) is the admin API's job (400 typed error); we don't re-check
 * rules here. On success we revalidate the list path so the RSC re-fetches.
 *
 * Upstream rows are parsed from repeated same-name form fields via
 * FormData.getAll() and zipped by position — see create-form.tsx /
 * upstream-row.tsx for why (removing a mid-list row just shrinks the
 * parallel arrays; no index renumbering needed).
 */
export async function createModel(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const alias = String(formData.get("alias") ?? "").trim();
  if (!alias) {
    const mapped = mapBackendError("model alias is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const description = String(formData.get("description") ?? "").trim();
  const contextLengthRaw = String(formData.get("context_length") ?? "").trim();
  const capabilities = parseList(String(formData.get("capabilities") ?? ""));
  const tags = parseList(String(formData.get("tags") ?? ""));

  const providers = formData.getAll("upstream_provider").map(String);
  const models = formData.getAll("upstream_model").map(String);
  const maxTokens = formData.getAll("upstream_max_tokens").map(String);
  const promptRaw = formData.getAll("upstream_prompt_price").map(String);
  const completionRaw = formData
    .getAll("upstream_completion_price")
    .map(String);
  const cacheMulRaw = formData
    .getAll("upstream_cache_hit_multiplier")
    .map(String);

  let upstreams: ModelUpstream[];
  try {
    upstreams = providers.map((provider, i) => {
      const u: ModelUpstream = {
        provider,
        upstream_model: models[i] ?? "",
      };
      const mt = Number(maxTokens[i]);
      if (mt > 0) u.default_max_tokens = mt;
      const prompt = promptRaw[i]?.trim();
      const completion = completionRaw[i]?.trim();
      // cache_hit_multiplier: UI percent → micro-units (100% = 1_000_000).
      // Empty/0 = unconfigured = full price (not sent).
      const cacheMulPct = Number(cacheMulRaw[i]?.trim() ?? "");
      if (prompt || completion) {
        u.pricing = {
          prompt_per_1m: prompt ? displayToMicro(prompt) : 0,
          completion_per_1m: completion ? displayToMicro(completion) : 0,
          currency: "USD", // single-currency-per-deployment (ADR-0013)
        };
        if (Number.isFinite(cacheMulPct) && cacheMulPct > 0) {
          u.pricing.cache_hit_multiplier = Math.min(
            Math.max(cacheMulPct * 10_000, 0),
            1_000_000,
          );
        }
      }
      return u;
    });
  } catch {
    // Not a backend message (displayToMicro throws locally on malformed
    // input) — construct the FormResult directly instead of routing through
    // mapBackendError, which maps *backend* strings.
    return {
      ok: false,
      error: "invalid pricing amount",
      errorKey: "invalidPricingAmount",
    };
  }

  try {
    const client = await serverAdminClient();
    const body: Record<string, unknown> = { alias, upstreams };
    if (description) body.description = description;
    if (contextLengthRaw) {
      const cl = Number(contextLengthRaw);
      if (cl > 0) body.context_length = cl;
    }
    if (capabilities.length > 0) body.capabilities = capabilities;
    if (tags.length > 0) body.tags = tags;
    const { error, response } = await client.POST("/api/v1/models", {
      body: body as never,
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/models");
  return { ok: true };
}

export async function updateModel(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const alias = String(formData.get("alias") ?? "").trim();
  if (!alias) {
    const mapped = mapBackendError("model alias is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const description = String(formData.get("description") ?? "").trim();
  const contextLengthRaw = String(formData.get("context_length") ?? "").trim();
  const capabilities = parseList(String(formData.get("capabilities") ?? ""));
  const tags = parseList(String(formData.get("tags") ?? ""));

  const providers = formData.getAll("upstream_provider").map(String);
  const models = formData.getAll("upstream_model").map(String);
  const maxTokens = formData.getAll("upstream_max_tokens").map(String);
  const promptRaw = formData.getAll("upstream_prompt_price").map(String);
  const completionRaw = formData
    .getAll("upstream_completion_price")
    .map(String);
  const cacheMulRaw = formData
    .getAll("upstream_cache_hit_multiplier")
    .map(String);

  let upstreams: ModelUpstream[];
  try {
    upstreams = providers.map((provider, i) => {
      const u: ModelUpstream = {
        provider,
        upstream_model: models[i] ?? "",
      };
      const mt = Number(maxTokens[i]);
      if (mt > 0) u.default_max_tokens = mt;
      const prompt = promptRaw[i]?.trim();
      const completion = completionRaw[i]?.trim();
      const cacheMulPct = Number(cacheMulRaw[i]?.trim() ?? "");
      if (prompt || completion) {
        u.pricing = {
          prompt_per_1m: prompt ? displayToMicro(prompt) : 0,
          completion_per_1m: completion ? displayToMicro(completion) : 0,
          currency: "USD",
        };
        if (Number.isFinite(cacheMulPct) && cacheMulPct > 0) {
          u.pricing.cache_hit_multiplier = Math.min(
            Math.max(cacheMulPct * 10_000, 0),
            1_000_000,
          );
        }
      }
      return u;
    });
  } catch {
    return {
      ok: false,
      error: "invalid pricing amount",
      errorKey: "invalidPricingAmount",
    };
  }

  try {
    const client = await serverAdminClient();
    // In edit mode, always send the metadata fields so they can be cleared
    // (empty string / empty list overwrites). PatchModel treats present keys
    // as "set to this value" (ADR-0030).
    const body: Record<string, unknown> = {
      description,
      capabilities,
      tags,
      upstreams,
    };
    if (contextLengthRaw) {
      const cl = Number(contextLengthRaw);
      body.context_length = cl > 0 ? cl : 0;
    } else {
      body.context_length = 0;
    }
    const { error, response } = await client.PATCH("/api/v1/models/{alias}", {
      params: { path: { alias } },
      body: body as never,
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "update failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/models");
  return { ok: true };
}

export async function deleteModel(alias: string): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/models/{alias}",
      { params: { path: { alias } } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "delete failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/models");
  return { ok: true };
}
