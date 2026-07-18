import "server-only";

import {
  type AdminClient,
  createAdminClient,
} from "@voxeltoad/gateway-sdk/admin";
import { getToken } from "@/lib/session";

/**
 * serverAdminClient builds a typed admin client bound to the current operator's
 * session token (design/frontend.md §3). SERVER-ONLY: the generated client and
 * the token never reach the browser (ADR-0020). Every RSC read and Server
 * Action write goes through this.
 *
 * The admin base URL comes from ADMIN_URL; the Next→admin hop is server-to-
 * server on the internal network, where the Bearer token is appropriate.
 */
export async function serverAdminClient(): Promise<AdminClient> {
  const baseUrl = process.env.ADMIN_URL;
  if (!baseUrl) {
    throw new Error("ADMIN_URL must be set (see web/.env.example)");
  }
  const token = await getToken();
  return createAdminClient({ baseUrl, token });
}

// anonAdminClient is for the one unauthenticated call: POST /auth/login.
export function anonAdminClient(): AdminClient {
  const baseUrl = process.env.ADMIN_URL;
  if (!baseUrl) {
    throw new Error("ADMIN_URL must be set (see web/.env.example)");
  }
  return createAdminClient({ baseUrl });
}
