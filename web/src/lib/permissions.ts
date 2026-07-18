import type { SessionData } from "@/lib/session";

/**
 * Permission utility for the Control Panel frontend (Phase-2 RBAC).
 * The back-end 403 remains the real boundary; these helpers allow the UI to
 * hide/show elements BEFORE sending a request, avoiding unnecessary errors.
 */

/** has reports whether the session carries the given permission (or wildcard). */
export function has(session: SessionData, perm: string): boolean {
  if (!session.permissions || session.permissions.length === 0) return false;
  if (session.permissions.includes("*")) return true;
  return session.permissions.includes(perm);
}

/** hasAny reports whether the session carries ANY of the given permissions. */
export function hasAny(session: SessionData, ...perms: string[]): boolean {
  return perms.some((p) => has(session, p));
}
