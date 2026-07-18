import createClient, { type Client } from "openapi-fetch";
import type { components, paths } from "./admin-schema";

/**
 * Typed admin (management-plane) client, generated from the authoritative
 * OpenAPI spec (docs/openapi/admin.yaml, ADR-0019 OpenAPI-first). The `paths`
 * types are regenerated via `npm run codegen`; every request/response is
 * checked against the published contract at compile time.
 *
 * This is a SEPARATE entry point from the data-plane client (VoxeltoadGateway):
 * different audience (operators vs. applications) and different auth (operator
 * session vs. client API key). Import it via `@voxeltoad/gateway-sdk/admin`
 * so consumers that only need one plane don't pull in the other's deps.
 *
 * It serves two roles from one source of truth: the contract test drives it
 * against a live server, and the Control Panel UI uses it as its API client —
 * so "tests pass" means "the UI's types match the real backend".
 */
export interface AdminClientOptions {
  /** Admin plane base URL, e.g. "http://localhost:8090". */
  baseUrl: string;
  /**
   * Operator session token from POST /auth/login (sent as a Bearer header).
   *
   * SECURITY: for a browser UI calling the admin API directly, a raw bearer
   * token in JS-readable storage (localStorage) is XSS-exfiltratable and the
   * operator token is high-privilege. Prefer an httpOnly+SameSite session
   * cookie + CSRF token instead; pass `credentials: "include"` and omit
   * `token`. See docs/adr for the session-transport decision.
   */
  token?: string;
  /**
   * Forwarded to fetch. Set to "include" when authenticating via cookie so the
   * browser attaches the admin session cookie on cross-origin requests (pairs
   * with the server's CORS Allow-Credentials + specific origin).
   */
  credentials?: RequestCredentials;
}

/** Creates a typed admin client. */
export function createAdminClient(options: AdminClientOptions): Client<paths> {
  const headers: Record<string, string> = {};
  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }
  return createClient<paths>({
    baseUrl: options.baseUrl,
    headers,
    credentials: options.credentials,
  });
}

/** The uniform error envelope every admin endpoint returns on failure. */
export type AdminErrorBody = components["schemas"]["Error"];

/**
 * AdminError is thrown by {@link unwrap} when a request fails. It surfaces the
 * uniform error envelope ({error:{message,type}}) plus the HTTP status, so call
 * sites (and the UI) can branch on `type`/`status` instead of hand-inspecting
 * every `{ data, error }` result.
 */
export class AdminError extends Error {
  readonly status: number;
  readonly type: string;

  constructor(status: number, body: AdminErrorBody | undefined) {
    const message = body?.error?.message ?? `admin request failed (${status})`;
    super(message);
    this.name = "AdminError";
    this.status = status;
    this.type = body?.error?.type ?? "api_error";
  }
}

/**
 * unwrap turns an openapi-fetch result into its data or throws AdminError. It
 * lets call sites write `const t = unwrap(await client.POST(...))` instead of
 * repeating the `if (error) ...` dance, while preserving full response typing.
 */
export function unwrap<T>(result: {
  data?: T;
  error?: AdminErrorBody;
  response: Response;
}): T {
  if (result.error !== undefined || !result.response.ok) {
    throw new AdminError(result.response.status, result.error);
  }
  // 204 No Content and similar have no body; data is undefined by design.
  return result.data as T;
}

/**
 * unwrapJson is unwrap for endpoints whose 200 response advertises BOTH a JSON
 * body and a CSV export (e.g. /usage, /usage/summary, /request-logs declare
 * `application/json: <Page>` + `text/csv: string`). openapi-typescript unions
 * those into `string | <Page>`, so plain `unwrap` returns a union on which
 * `.data`/`.total` don't type-check. These call sites always request JSON (no
 * `format=csv`), so narrow the union by excluding the string (CSV) arm.
 */
export function unwrapJson<T>(result: {
  data?: T;
  error?: AdminErrorBody;
  response: Response;
}): Exclude<T, string> {
  return unwrap(result) as Exclude<T, string>;
}

export type { paths as AdminPaths } from "./admin-schema";
export type AdminClient = Client<paths>;
