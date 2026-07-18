"use server";

import { getLocale } from "next-intl/server";
import { createAdminClient } from "@voxeltoad/gateway-sdk/admin";
import { anonAdminClient } from "@/lib/admin";
import { setSession, type OperatorRole, type SessionData } from "@/lib/session";
import type { FormResult } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";
import { redirect } from "@/i18n/navigation";

/**
 * loginAction authenticates the operator, fetches their identity from
 * /api/v1/me to learn their role + tenant_name, and redirects to the
 * first allowed page for that role (super-admin → /providers,
 * tenant-admin → /api-keys).
 */
export async function loginAction(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const email = String(formData.get("email") ?? "");
  const password = String(formData.get("password") ?? "");
  if (!email || !password) {
    const mapped = mapBackendError("email and password are required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const client = anonAdminClient();
  let data: { token?: string } | undefined;
  let error: { error?: { message?: string } } | undefined;
  let response: Response;
  try {
    const result = await client.POST("/auth/login", {
      body: { email, password },
    });
    data = result.data;
    error = result.error;
    response = result.response;
  } catch (fetchErr) {
    // Next.js 16 + Node 26 may throw on 4xx JSON responses due to a fetch
    // body-handling quirk. Treat it as "invalid credentials" (the only
    // expected failure mode for a POST with email+password).
    return { ok: false, error: "invalid credentials" };
  }

  if (error || !response.ok || !data?.token) {
    const message =
      error?.error?.message ??
      (response.status === 429
        ? "too many failed logins; try again later"
        : "invalid credentials");
    const mapped = mapBackendError(message);
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const token = data.token;
  // Fetch operator identity with the freshly-issued token (not from the
  // session cookie, which isn't set yet — use a direct admin client).
  const baseUrl = process.env.ADMIN_URL || "";
  const meClient = createAdminClient({ baseUrl, token });
  const meResp = await meClient.GET("/api/v1/me");

  if (meResp.error || !meResp.data) {
    const message =
      meResp.error?.error?.message ?? "identity lookup failed; try again";
    const mapped = mapBackendError(message);
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  const meData = meResp.data as Record<string, unknown>;
  const role = (meData.role ?? "super-admin") as OperatorRole;
  const roleID = meData.role_id as number | undefined;
  const scopeKind = meData.scope_kind as "global" | "tenant" | undefined;
  const permissions = meData.permissions as string[] | undefined;
  const tenantName = (meData.tenant_name ?? undefined) as string | undefined;

  await setSession({
    token, email, role, roleID, scopeKind, permissions, tenantName,
  } satisfies SessionData);
  const locale = await getLocale();

  // Redirect to the first allowed page based on permissions.
  if (permissions && permissions.length > 0) {
    if (permissions.includes("provider.read") || permissions.includes("*")) {
      redirect({ href: "/providers", locale });
    }
    if (permissions.includes("api_key.read")) {
      redirect({ href: "/api-keys", locale });
    }
    if (permissions.includes("usage.read")) {
      redirect({ href: "/usage", locale });
    }
  }
  // Fallback for legacy roles.
  if (role === "tenant-admin") {
    redirect({ href: "/api-keys", locale });
  }
  redirect({ href: "/providers", locale });
  return { ok: true };
}
