import "server-only";

import { redirect } from "next/navigation";
import { AdminError } from "@voxeltoad/gateway-sdk/admin";
import { clearSession } from "@/lib/session";

/**
 * Error handling for admin calls (design/frontend.md §4).
 *
 * A 401 means the back-end session was revoked or expired (the authoritative
 * `sessions` table, 12h TTL) — our encrypted cookie still carries a now-dead
 * token. We bounce to /login. Everything else is surfaced to the caller (typed
 * AdminError) so a form can show {error:{message,type}} — value validation is
 * the back-end's job (design/frontend.md §8), not re-done here.
 *
 * CONTEXT SPLIT: cookie modification is only allowed in a Server Action or
 * Route Handler — NOT during RSC render. So the two helpers below differ:
 *  - onAuthExpired    → called from RSC renders; redirects to /logout (a Route
 *                       Handler) which clears the cookie and bounces to /login.
 *  - toFormError      → called from Server Actions; clears the cookie directly
 *                       then redirects to /login (Server Actions can set cookies).
 */

/**
 * onAuthExpired redirects to /logout when err is a 401. Call it in a catch
 * around a server-side admin read inside an RSC render. It does not return on a
 * 401 (redirect throws); for other errors it re-throws.
 *
 * Why redirect to /logout (a Route Handler) instead of clearing the cookie
 * here: RSC renders cannot modify cookies — only Server Actions and Route
 * Handlers can. The /logout handler clears the session and bounces to /login.
 */
export async function onAuthExpired(err: unknown): Promise<never> {
  if (err instanceof AdminError && err.status === 401) {
    redirect("/logout");
  }
  throw err;
}

/**
 * Outcome returned by handleAdminError when the caller lacks permission. The
 * page renders a localized no-permission notice (see ForbiddenNotice) instead
 * of crashing. `message` is the raw backend message — usually an apperr i18n
 * key like "errors.auth.superAdminRequired" — to be fed through mapBackendError.
 */
export interface ForbiddenOutcome {
  kind: "forbidden";
  message: string;
}

/**
 * handleAdminError is like onAuthExpired but also catches 403s gracefully:
 *  - 401 → redirect to /logout (never returns, same as onAuthExpired)
 *  - 403 → returns a ForbiddenOutcome so the page can render a no-permission UI
 *  - anything else → re-throws (typed AdminError)
 *
 * Use this in RSC page `catch` blocks where a tenant-admin may reach a
 * super-admin-only endpoint by direct URL. The back-end 403 remains the real
 * boundary (layout.tsx); this just renders a friendly region instead of an
 * uncaught AdminError (design/domain-flows.md §权限不足).
 */
export async function handleAdminError(
  err: unknown,
): Promise<ForbiddenOutcome> {
  if (err instanceof AdminError) {
    if (err.status === 401) {
      redirect("/logout");
    }
    if (err.status === 403) {
      return { kind: "forbidden", message: err.message };
    }
  }
  throw err;
}

/** FormResult is what Server Actions return to a client form.
 *
 * - `ok: true`  → success
 * - `ok: false` → error; `error` is the English fallback, `errorKey` is the
 *   optional i18n key for translating the message (design/frontend.md §12).
 */
export type FormResult =
  | { ok: true }
  | { ok: false; error: string; errorKey?: string };

/**
 * toFormError maps an admin failure to a FormResult a form can render. A 401
 * still clears+redirects; other AdminErrors become an inline message.
 *
 * Only call this from Server Actions (where cookie modification is allowed).
 */
export async function toFormError(err: unknown): Promise<FormResult> {
  if (err instanceof AdminError) {
    if (err.status === 401) {
      await clearSession();
      redirect("/login");
    }
    return { ok: false, error: err.message };
  }
  return { ok: false, error: "unexpected error" };
}
