import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { OverviewPageClient } from "./client";

export const dynamic = "force-dynamic";

export default async function OverviewPage() {
  let data: Record<string, unknown> = {};
  try {
    const client = await serverAdminClient();
    const page = unwrap(await client.GET("/api/v1/overview"));
    data = (page ?? {}) as Record<string, unknown>;
  } catch (err) {
    // 401 redirects to /logout; a 403 (super-admin-only endpoint reached by a
    // tenant-admin via direct URL) renders a no-permission notice instead of
    // crashing the render; anything else re-throws.
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <OverviewPageClient data={data} />
    </div>
  );
}
