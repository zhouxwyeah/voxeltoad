import { serverAdminClient } from "@/lib/admin";
import { handleAdminError } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { SettingsClient } from "./client";

export const dynamic = "force-dynamic";

type GatewaySettings = {
  trace?: {
    capture_payload_enabled?: boolean;
    max_body_kb?: number;
    retention_days?: number;
  };
};

/**
 * Gateway settings page (super-admin). Fetches the current hot-reloadable
 * behavior settings; the client form PUTs updates via a Server Action.
 */
export default async function SettingsPage() {
  let settings: GatewaySettings = {};
  try {
    const client = await serverAdminClient();
    settings = unwrap(
      await client.GET("/api/v1/gateway-settings"),
    ) as GatewaySettings;
  } catch (err) {
    const outcome = await handleAdminError(err);
    return (
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
        <ForbiddenNotice message={outcome.message} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <SettingsClient initial={settings} />
    </div>
  );
}
