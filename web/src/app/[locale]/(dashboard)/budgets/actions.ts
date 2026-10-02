"use server";

import { revalidatePath } from "next/cache";
import { getTranslations } from "next-intl/server";
import { unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { serverAdminClient } from "@/lib/admin";
import { getSession } from "@/lib/session";
import { has } from "@/lib/permissions";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";
import { displayToMicro } from "@/lib/money";

import type { BudgetPolicy } from "./types";

type BudgetSpec = AdminPaths["/api/v1/budgets"]["post"]["requestBody"]["content"]["application/json"];
type BudgetPatch = Pick<BudgetPolicy, "version"> & Partial<Pick<BudgetPolicy, "limit" | "enabled">>;
type Resolution = AdminPaths["/api/v1/billing-reservations/{id}/resolve"]["post"]["requestBody"]["content"]["application/json"];

async function writeGuard(tenant: string, permission: string): Promise<FormResult | null> {
  const session = await getSession();
  const t = await getTranslations("budgets");
  if (session.scopeKind !== "global" || !has(session, permission)) {
    return { ok: false, error: t("forbiddenWrite") };
  }
  if (!tenant.trim()) return { ok: false, error: t("selectTenant") };
  return null;
}

async function actionError(err: unknown): Promise<FormResult> {
  const t = await getTranslations("budgets");
  if (err instanceof RangeError) return { ok: false, error: t("invalidAmount") };
  const result = await toFormError(err);
  if (result.ok) return result;
  const mapped = mapBackendError(result.error);
  return { ok: false, error: mapped.fallback, errorKey: mapped.key };
}

function amount(input: FormDataEntryValue | null): number {
  const value = displayToMicro(String(input ?? ""));
  if (!Number.isSafeInteger(value)) throw new RangeError("unsafe monetary amount");
  return value;
}

export async function createBudget(tenant: string, form: FormData): Promise<FormResult> {
  const denied = await writeGuard(tenant, "budget.write");
  if (denied) return denied;
  try {
    const body: BudgetSpec = {
      name: String(form.get("name") ?? "").trim(),
      scope_kind: String(form.get("scope_kind")) as BudgetSpec["scope_kind"],
      scope_ref: String(form.get("scope_ref") ?? "").trim(),
      environment: String(form.get("environment") ?? "") as BudgetSpec["environment"],
      period: String(form.get("period")) as BudgetSpec["period"],
      timezone: String(form.get("timezone") ?? "UTC").trim(),
      currency: String(form.get("currency")) as BudgetSpec["currency"],
      limit: amount(form.get("limit")),
      mode: String(form.get("mode")) as BudgetSpec["mode"],
      thresholds: String(form.get("thresholds") ?? "").split(",").filter((item) => item.trim() !== "").map(Number),
    };
    const client = await serverAdminClient();
    unwrap(await client.POST("/api/v1/budgets", { params: { query: { tenant } }, body }));
  } catch (err) {
    return actionError(err);
  }
  revalidatePath("/[locale]/budgets", "page");
  revalidatePath("/[locale]/budgets/[id]", "page");
  return { ok: true };
}

export async function updateBudget(tenant: string, id: number, version: number, form: FormData): Promise<FormResult> {
  const denied = await writeGuard(tenant, "budget.write");
  if (denied) return denied;
  try {
    const body: BudgetPatch = { version, enabled: form.get("enabled") === "true" };
    // An unchanged display value must not round a micro-unit limit on save.
    if (form.get("limitChanged") === "true") body.limit = amount(form.get("limit"));
    const client = await serverAdminClient();
    unwrap(await client.PATCH("/api/v1/budgets/{id}", { params: { path: { id }, query: { tenant } }, body }));
  } catch (err) {
    return actionError(err);
  }
  revalidatePath("/[locale]/budgets", "page");
  revalidatePath("/[locale]/budgets/[id]", "page");
  return { ok: true };
}

export async function resolveReservation(tenant: string, id: string, version: number, form: FormData): Promise<FormResult> {
  const denied = await writeGuard(tenant, "budget.resolve");
  if (denied) return denied;
  try {
    const action = String(form.get("action")) as Resolution["action"];
    const body: Resolution = {
      version,
      action,
      reason: String(form.get("reason") ?? "").trim(),
      evidence: String(form.get("evidence") ?? "").trim(),
      ...(action === "settle" ? { actual: amount(form.get("actual")) } : {}),
    };
    const client = await serverAdminClient();
    unwrap(await client.POST("/api/v1/billing-reservations/{id}/resolve", { params: { path: { id }, query: { tenant } }, body }));
  } catch (err) {
    return actionError(err);
  }
  revalidatePath("/[locale]/budgets", "page");
  revalidatePath("/[locale]/budgets/[id]", "page");
  return { ok: true };
}
