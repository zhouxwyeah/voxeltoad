import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { OperatorsPageClient } from "./client";

// Per-request: reads the session cookie + calls the admin API. Never prerender.
export const dynamic = "force-dynamic";

type OperatorRow = {
  id: number;
  email: string;
  role: string;
  tenant_id?: number | null;
};

type TenantOption = { id: number; name: string };

type RoleOption = { id: number; name: string; scope_kind: string };

/**
 * Operators list RSC. Fetches operators + tenants + roles server-side, then
 * hands all to the client shell. Supports create (with custom roles via
 * role_id), list, edit, and delete (super-admin only).
 */
export default async function OperatorsPage({
  searchParams,
}: {
  searchParams: Promise<{ cursor?: string; limit?: string }>;
}) {
  const { cursor, limit } = await searchParams;

  let rows: OperatorRow[] = [];
  let tenants: TenantOption[] = [];
  let roles: RoleOption[] = [];
  try {
    const client = await serverAdminClient();
    const query: Record<string, string | number> = {};
    if (cursor) query.cursor = cursor;
    if (limit) query.limit = Number(limit);

    const page = unwrap(
      await client.GET("/api/v1/operators", { params: { query } }),
    );
    rows = (page.data ?? []) as OperatorRow[];

    const tenantPage = unwrap(
      await client.GET("/api/v1/tenants", { params: { query: {} } }),
    );
    tenants = (tenantPage.data ?? []) as TenantOption[];

    const rolesPage = unwrap(
      await client.GET("/api/v1/roles", {}),
    );
    roles = (rolesPage.data ?? []) as RoleOption[];
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <OperatorsPageClient rows={rows} tenants={tenants} roles={roles} />
    </div>
  );
}
