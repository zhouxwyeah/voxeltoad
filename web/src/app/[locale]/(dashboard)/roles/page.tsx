import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { RolesPageClient } from "./client";

export const dynamic = "force-dynamic";

export type RoleRow = {
  id: number;
  name: string;
  scope_kind: "global" | "tenant";
  is_builtin: boolean;
  description: string;
  permissions: string[];
};

export type PermissionItem = {
  perm: string;
  scope: string;
  label: string;
};

export default async function RolesPage() {
  let roles: RoleRow[] = [];
  let permissions: PermissionItem[] = [];

  try {
    const client = await serverAdminClient();
    const rolesResp = unwrap(await client.GET("/api/v1/roles", {}));
    roles = (rolesResp.data ?? []) as RoleRow[];

    const permsResp = unwrap(await client.GET("/api/v1/permissions", {}));
    permissions = (permsResp.data ?? []) as PermissionItem[];
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
      <RolesPageClient roles={roles} permissions={permissions} />
    </div>
  );
}
