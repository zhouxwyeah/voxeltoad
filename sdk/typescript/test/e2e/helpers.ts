import { expect } from "vitest";
import {
  type AdminClient,
  AdminError,
  createAdminClient,
  unwrap,
  unwrapJson,
} from "../../src/admin";

/**
 * Shared helpers for admin e2e contract tests. Each test file opt-ins via
 * VOXELTOAD_ADMIN_E2E=1 and reuses these helpers to log in, mint unique resource
 * names, and assert the uniform error envelope — so individual test files stay
 * focused on the resource contract under test.
 */
const baseUrl = process.env.VOXELTOAD_ADMIN_BASE_URL ?? "http://localhost:8090";
const email = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@local";
const password = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "change-me";

export const ADMIN_BASE_URL = baseUrl;

/** A super-admin client authenticated via /auth/login. Shared across tests in
 *  a file via #loginSuperAdmin() (called from beforeAll). */
export async function loginSuperAdmin(): Promise<AdminClient> {
  const anon = createAdminClient({ baseUrl });
  const { data, error } = await anon.POST("/auth/login", {
    body: { email, password },
  });
  if (error)
    throw new Error(`super-admin login failed: ${JSON.stringify(error)}`);
  if (!data?.token) throw new Error("super-admin login returned no token");
  return createAdminClient({ baseUrl, token: data.token });
}

/** Log in as an arbitrary operator (used for tenant-admin RBAC tests). */
export async function loginAs(
  opEmail: string,
  opPassword: string,
): Promise<AdminClient> {
  const anon = createAdminClient({ baseUrl });
  const { data, error } = await anon.POST("/auth/login", {
    body: { email: opEmail, password: opPassword },
  });
  if (error)
    throw new Error(`login as ${opEmail} failed: ${JSON.stringify(error)}`);
  if (!data?.token) throw new Error(`login as ${opEmail} returned no token`);
  return createAdminClient({ baseUrl, token: data.token });
}

/** Mint a unique resource name for a test run. */
export function unique(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
}

/** Assert that an openapi-fetch result failed with a typed AdminError. */
export function expectAdminError(
  status: number,
  type?: string,
): (err: unknown) => void {
  return (err: unknown) => {
    if (!(err instanceof AdminError)) {
      throw new Error(`expected AdminError, got ${err}`);
    }
    expect(err.status).toBe(status);
    if (type !== undefined) {
      expect(err.type).toBe(type);
    }
  };
}

/** Re-export unwrap so tests don't need a second import line. */
export { unwrap, unwrapJson };
