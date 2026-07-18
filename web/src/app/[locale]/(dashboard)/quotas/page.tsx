import { serverAdminClient } from "@/lib/admin";
import { getSession } from "@/lib/session";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { QuotasPageClient } from "./client";

export const dynamic = "force-dynamic";

export default async function QuotasPage() {
  const session = await getSession();
  const tenantName = session.tenantName ?? "";
  const scope = tenantName ? `tenant:${tenantName}` : "";

  let balance = 0;
  let currency = "";
  let fetchError = "";
  if (scope) {
    try {
      const client = await serverAdminClient();
      const data = unwrap(
        await client.GET("/api/v1/quotas", {
          params: { query: { scope } },
        }),
      );
      balance = (data as { balance?: number })?.balance ?? 0;
      currency = (data as { currency?: string })?.currency ?? "";
    } catch (err) {
      if (err instanceof Error) {
        fetchError = err.message;
      }
    }
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <QuotasPageClient
        scope={scope}
        balance={balance}
        currency={currency}
        fetchError={fetchError}
      />
    </div>
  );
}
