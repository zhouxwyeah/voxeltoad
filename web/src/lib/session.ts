import "server-only";

import { cookies } from "next/headers";
import { getIronSession, type SessionOptions } from "iron-session";
import type { AdminPaths } from "@voxeltoad/gateway-sdk/admin";

/**
 * Server-side session for the Control Panel (ADR-0020, design/frontend.md §4).
 *
 * The high-privilege operator token from POST /auth/login is held ONLY on the
 * Next server, encrypted into an httpOnly+SameSite+Secure cookie (iron-session).
 * It never reaches browser JS, so it is not XSS-exfiltratable. The authoritative
 * revocation layer remains the back-end `sessions` table (12h TTL); this cookie
 * is just an encrypted envelope carrying the token between requests.
 */

// Role mirrors the operator roles the admin API enforces (ADR-0017). Phase-2
// RBAC allows custom role names; the string is kept as a display label while
// permissions[] and scopeKind drive authorization. super-admin/tenant-admin
// remain the built-in names.
export type OperatorRole = string;

export interface SessionData {
  token?: string;
  email?: string;
  role?: OperatorRole;
  roleID?: number;
  scopeKind?: "global" | "tenant";
  permissions?: string[];
  tenantName?: string;
}

// Non-secret operator identity, derived from the login response's token by a
// follow-up lookup. Kept minimal on purpose.
const COOKIE_NAME = "voxeltoad_admin_session";
// Built per-request (not at module load) so a missing SESSION_SECRET fails at
// request time, not during build page-data collection.
function sessionOptions(): SessionOptions {
  return {
    password: requireSessionSecret(),
    cookieName: COOKIE_NAME,
    cookieOptions: {
      httpOnly: true,
      sameSite: "lax",
      // Secure in production; relaxed for local http dev (Playwright hits http).
      secure: process.env.NODE_ENV === "production",
      path: "/",
    },
  };
}

function requireSessionSecret(): string {
  const secret = process.env.SESSION_SECRET;
  if (!secret || secret.length < 32) {
    throw new Error(
      "SESSION_SECRET must be set and at least 32 characters (see web/.env.example)",
    );
  }
  return secret;
}

// getSession returns the iron-session handle. Server-only (uses next/headers).
export async function getSession() {
  const cookieStore = await cookies();
  return getIronSession<SessionData>(cookieStore, sessionOptions());
}

/** getToken returns the operator token, or undefined when unauthenticated. */
export async function getToken(): Promise<string | undefined> {
  const session = await getSession();
  return session.token;
}

/** setSession stores the token + identity after a successful login. */
export async function setSession(data: SessionData): Promise<void> {
  const session = await getSession();
  session.token = data.token;
  session.email = data.email;
  session.role = data.role;
  session.roleID = data.roleID;
  session.scopeKind = data.scopeKind;
  session.permissions = data.permissions;
  session.tenantName = data.tenantName;
  await session.save();
}

/** clearSession destroys the cookie (logout, or after a back-end 401). */
export async function clearSession(): Promise<void> {
  const session = await getSession();
  session.destroy();
}

// Re-export the generated path types so callers share the single source of
// truth (the OpenAPI spec) without importing the SDK directly everywhere.
export type { AdminPaths };
