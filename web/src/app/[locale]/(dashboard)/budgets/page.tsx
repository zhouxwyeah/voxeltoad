import { getTranslations } from "next-intl/server";
import { unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { serverAdminClient } from "@/lib/admin";
import { getSession } from "@/lib/session";
import { has } from "@/lib/permissions";
import { handleAdminError } from "@/lib/errors";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import { BudgetsClient } from "./client";
import type { BudgetPolicy, BudgetEvent, Reservation, BudgetView } from "./types";

type Tenant = AdminPaths["/api/v1/tenants"]["post"]["responses"][201]["content"]["application/json"];

export const dynamic = "force-dynamic";

export default async function BudgetsPage({ searchParams }: {
  searchParams: Promise<{ tenant?: string; cursor?: string; view?: string; status?: string }>;
}) {
  const params = await searchParams;
  const session = await getSession();
  const t = await getTranslations("budgets");
  if (!has(session, "budget.read")) {
    return <div className="mx-auto max-w-5xl p-8"><ForbiddenNotice message={t("forbiddenRead")} /></div>;
  }
  const global = session.scopeKind === "global";
  const tenant = global ? (params.tenant ?? "").trim() : session.tenantName ?? "";
  const view: BudgetView = params.view === "events" || params.view === "reservations" ? params.view : "policies";
  const status = (params.status ?? "unknown") as Reservation["status"] | "all";
  let policies: BudgetPolicy[] = [];
  let events: BudgetEvent[] = [];
  let reservations: Reservation[] = [];
  let nextCursor = "";
  let tenants: string[] = [];
  try {
    const client = await serverAdminClient();
    if (global && has(session, "tenant.read")) {
      const list = unwrap(await client.GET("/api/v1/tenants", { params: { query: { limit: 500 } } }));
      const tenantRows: Tenant[] = list.data ?? [];
      tenants = tenantRows.map((row) => row.name);
    }
    if (!global || tenant) {
      const query = { ...(global ? { tenant } : {}), ...(params.cursor ? { cursor: params.cursor } : {}), limit: 50 };
      if (view === "events") {
        const result = unwrap(await client.GET("/api/v1/budget-events", { params: { query } }));
        events = result.data ?? [];
        nextCursor = result.next_cursor ?? "";
      } else if (view === "reservations") {
        const result = unwrap(await client.GET("/api/v1/billing-reservations", { params: { query: { ...query, ...(status && status !== "all" ? { status } : {}) } } }));
        reservations = (result.data ?? []).map(({ id, request_id, status, estimate, actual, currency, reason, version, created_at }) => ({ id, request_id, status, estimate, actual, currency, reason, version, created_at }));
        nextCursor = result.next_cursor ?? "";
      } else {
        const result = unwrap(await client.GET("/api/v1/budgets", { params: { query } }));
        policies = result.data ?? [];
        nextCursor = result.next_cursor ?? "";
      }
    }
  } catch (err) {
    const outcome = await handleAdminError(err);
    return <div className="mx-auto max-w-5xl p-8"><ForbiddenNotice message={outcome.message} /></div>;
  }
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <BudgetsClient key={`${tenant}:${view}`} tenant={tenant} global={global} tenants={tenants} view={view} status={status} policies={policies} events={events} reservations={reservations} nextCursor={nextCursor} canWrite={global && has(session, "budget.write")} canResolve={global && has(session, "budget.resolve")} />
    </div>
  );
}
