"use server";

import { revalidatePath } from "next/cache";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

/**
 * Provider write actions (design/frontend.md §5). Value validation is the admin
 * API's job (400 typed error); we don't re-check rules here. On success we
 * revalidate the list path so the RSC re-fetches.
 *
 * Create uses POST upsert; edit uses PATCH partial update (ADR-0030).
 */
export async function createProvider(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  if (!name) {
    const mapped = mapBackendError("name is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  const body: Record<string, unknown> = { name };
  const type = String(formData.get("type") ?? "").trim();
  if (type) body.type = type;
  const endpoints = collectEndpoints(formData);
  if (endpoints.length > 0) body.endpoints = endpoints;
  // api_key is write-only plaintext; when supplied the gateway encrypts and
  // stores it, and api_key_ref is rewritten to db://provider/<name> (ADR-0030).
  const apiKey = String(formData.get("api_key") ?? "").trim();
  if (apiKey) body.api_key = apiKey;
  const apiKeyRef = String(formData.get("api_key_ref") ?? "").trim();
  if (apiKeyRef) body.api_key_ref = apiKeyRef;
  const weight = String(formData.get("weight") ?? "").trim();
  if (weight) body.weight = Number(weight);

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST("/api/v1/providers", {
      // body is a superset-shaped Provider; the required field is name.
      body: body as { name: string },
    });
    if (error || !response.ok) {
      const message = error?.error?.message ?? "create failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/providers");
  return { ok: true };
}

export async function updateProvider(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const name = String(formData.get("name") ?? "").trim();
  if (!name) {
    const mapped = mapBackendError("name is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }
  // A plaintext api_key rotates the credential. The generic PATCH endpoint
  // (/providers/{name}) does not accept api_key (ADR-0030 keeps credential
  // writes on a dedicated endpoint), so when a key is supplied we: (a) PATCH
  // the non-secret fields on the generic endpoint, then (b) rotate the
  // credential via /providers/{name}/credential. We deliberately do not send
  // api_key_ref in this branch — the credential endpoint sets it to
  // db://provider/<name>, and the form's api_key_ref field may hold a masked
  // value we must not persist.
  const apiKey = String(formData.get("api_key") ?? "").trim();
  const body: Record<string, unknown> = {};
  const type = String(formData.get("type") ?? "").trim();
  if (type) body.type = type;
  const endpoints = collectEndpoints(formData);
  if (endpoints.length > 0) body.endpoints = endpoints;
  // Only send api_key_ref when NOT rotating via plaintext (ADR-0030).
  if (!apiKey) {
    const apiKeyRef = String(formData.get("api_key_ref") ?? "").trim();
    if (apiKeyRef) body.api_key_ref = apiKeyRef;
  }
  const weight = String(formData.get("weight") ?? "").trim();
  if (weight) body.weight = Number(weight);

  try {
    const client = await serverAdminClient();
    // Step (a): generic partial update for non-secret fields, if any.
    if (Object.keys(body).length > 0) {
      const { error, response } = await client.PATCH(
        "/api/v1/providers/{name}",
        {
          params: { path: { name } },
          // All fields optional per ProviderPatch; only send what's present.
          body: body as Record<string, never>,
        },
      );
      if (error || !response.ok) {
        const message = error?.error?.message ?? "update failed";
        const mapped = mapBackendError(message);
        return { ok: false, error: mapped.fallback, errorKey: mapped.key };
      }
    }
    // Step (b): rotate the credential when a plaintext key was supplied.
    if (apiKey) {
      const { error, response } = await client.PATCH(
        "/api/v1/providers/{name}/credential",
        {
          params: { path: { name } },
          body: { api_key: apiKey },
        },
      );
      if (error || !response.ok) {
        const message = error?.error?.message ?? "credential update failed";
        const mapped = mapBackendError(message);
        return { ok: false, error: mapped.fallback, errorKey: mapped.key };
      }
    }
  } catch (err) {
    return toFormError(err);
  }
  revalidatePath("/providers");
  return { ok: true };
}

export async function deleteProvider(name: string): Promise<FormResult> {
  try {
    const client = await serverAdminClient();
    const { error, response } = await client.DELETE(
      "/api/v1/providers/{name}",
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
  revalidatePath("/providers");
  return { ok: true };
}

/**
 * Outcome of a provider connectivity test. Unlike FormResult, success carries
 * probe data (latency/status); failure carries a displayable reason (probe
 * failures are free-form backend text, HTTP failures go through
 * mapBackendError for an optional i18n key). Never revalidates — a test
 * changes nothing.
 */
export type ProviderTestOutcome =
  | { ok: true; latencyMs: number; status?: number }
  | { ok: false; error: string; errorKey?: string };

/** Unsaved form values to probe (create/edit modal "测试连接"). */
export interface ProviderTestSpec {
  /** Existing provider whose stored credential is the fallback (edit modal). */
  name?: string;
  adapter: string;
  baseUrl: string;
  apiKey?: string;
  apiKeyRef?: string;
}

type TestResultBody = {
  ok: boolean;
  latency_ms: number;
  status?: number;
  error?: string;
};

function toOutcome(data: TestResultBody | undefined): ProviderTestOutcome {
  if (data?.ok) {
    return { ok: true, latencyMs: data.latency_ms, status: data.status };
  }
  return { ok: false, error: data?.error ?? "test failed" };
}

async function testHttpFailure(
  error: { error?: { message?: string } } | undefined,
): Promise<ProviderTestOutcome> {
  const mapped = mapBackendError(error?.error?.message ?? "test failed");
  return { ok: false, error: mapped.fallback, errorKey: mapped.key };
}

/** Probe a saved provider; the credential is resolved server-side. */
export async function testProvider(name: string): Promise<ProviderTestOutcome> {
  try {
    const client = await serverAdminClient();
    const { data, error, response } = await client.POST(
      "/api/v1/providers/{name}/test",
      { params: { path: { name } } },
    );
    if (error || !response.ok) {
      return testHttpFailure(error);
    }
    return toOutcome(data);
  } catch (err) {
    const fe = await toFormError(err);
    return fe.ok ? { ok: false, error: "unexpected error" } : fe;
  }
}

/** Probe unsaved form values; nothing is persisted server-side. */
export async function testProviderConnection(
  spec: ProviderTestSpec,
): Promise<ProviderTestOutcome> {
  try {
    const client = await serverAdminClient();
    const { data, error, response } = await client.POST(
      "/api/v1/provider-tests",
      {
        body: {
          adapter: spec.adapter,
          base_url: spec.baseUrl,
          ...(spec.name ? { name: spec.name } : {}),
          ...(spec.apiKey ? { api_key: spec.apiKey } : {}),
          ...(spec.apiKeyRef ? { api_key_ref: spec.apiKeyRef } : {}),
        },
      },
    );
    if (error || !response.ok) {
      return testHttpFailure(error);
    }
    return toOutcome(data);
  } catch (err) {
    const fe = await toFormError(err);
    return fe.ok ? { ok: false, error: "unexpected error" } : fe;
  }
}

/**
 * collectEndpoints reads the parallel arrays `endpoint_adapter` and
 * `endpoint_base_url` from the form (getAll → DOM order, same pattern as
 * upstream-row) and assembles them into an endpoints[] array. `endpoint_id` is
 * optional; when empty the backend derives it from the adapter (ADR-0049).
 * Rows with an empty adapter are dropped (the user deleted a row but the hidden
 * inputs remain).
 */
function collectEndpoints(formData: FormData): { id?: string; adapter: string; base_url: string }[] {
  const adapters = formData.getAll("endpoint_adapter");
  const baseUrls = formData.getAll("endpoint_base_url");
  const ids = formData.getAll("endpoint_id");
  const out: { id?: string; adapter: string; base_url: string }[] = [];
  for (let i = 0; i < adapters.length; i++) {
    const adapter = String(adapters[i] ?? "").trim();
    const base_url = String(baseUrls[i] ?? "").trim();
    if (!adapter && !base_url) continue;
    const id = String(ids[i] ?? "").trim();
    out.push({ ...(id ? { id } : {}), adapter, base_url });
  }
  return out;
}
