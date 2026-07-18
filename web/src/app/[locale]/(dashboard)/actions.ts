"use server";

import { redirect } from "next/navigation";
import { clearSession } from "@/lib/session";
import { serverAdminClient } from "@/lib/admin";
import { type FormResult, toFormError } from "@/lib/errors";
import { mapBackendError } from "@/lib/i18n-errors";

/** logoutAction clears the session cookie and returns to /login. */
export async function logoutAction(): Promise<void> {
  await clearSession();
  redirect("/login");
}

/** changePassword changes the current operator's own password. */
export async function changePassword(
  _prev: FormResult | null,
  formData: FormData,
): Promise<FormResult> {
  const password = String(formData.get("password") ?? "").trim();
  if (!password) {
    const mapped = mapBackendError("password is required");
    return { ok: false, error: mapped.fallback, errorKey: mapped.key };
  }

  try {
    const client = await serverAdminClient();
    const { error, response } = await client.POST(
      "/api/v1/operators/me/password",
      { body: { password } },
    );
    if (error || !response.ok) {
      const message = error?.error?.message ?? "password change failed";
      const mapped = mapBackendError(message);
      return { ok: false, error: mapped.fallback, errorKey: mapped.key };
    }
  } catch (err) {
    return toFormError(err);
  }
  return { ok: true };
}
