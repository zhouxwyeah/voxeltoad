import { getFormatter, getTranslations } from "next-intl/server";
import { ArrowLeft, ClipboardList } from "lucide-react";
import { unwrap, type AdminPaths } from "@voxeltoad/gateway-sdk/admin";
import { serverAdminClient } from "@/lib/admin";
import { getSession } from "@/lib/session";
import { has } from "@/lib/permissions";
import { handleAdminError } from "@/lib/errors";
import { microToDisplay } from "@/lib/money";
import { Button, Card, DetailField } from "@/components/ui";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { ForbiddenNotice } from "@/components/forbidden-notice";
import type { BudgetAccount, BudgetPolicy } from "../types";

export const dynamic = "force-dynamic";

export default async function BudgetDetail({ params, searchParams }: {
  params: Promise<{ id: string }>;
  searchParams: Promise<{ tenant?: string }>;
}) {
  const { id } = await params;
  const { tenant } = await searchParams;
  const session = await getSession();
  const t = await getTranslations("budgets");
  const format = await getFormatter();
  const global = session.scopeKind === "global";
  const query = global ? { tenant: tenant?.trim() ?? "" } : {};
  const back = `/budgets${global && tenant ? `?tenant=${encodeURIComponent(tenant)}` : ""}`;
  if (!has(session, "budget.read")) return <div className="mx-auto max-w-5xl p-8"><ForbiddenNotice message={t("forbiddenRead")} /></div>;
  if (global && !tenant?.trim()) return <div className="mx-auto max-w-5xl p-8"><EmptyState title={t("selectTenant")} action={<Button href="/budgets" variant="outline">{t("back")}</Button>} /></div>;
  let policy: BudgetPolicy;
  let accounts: BudgetAccount[];
  let quota: AdminPaths["/api/v1/quotas"]["get"]["responses"][200]["content"]["application/json"] | null = null;
  const quotaScope = `tenant:${global ? tenant?.trim() : session.tenantName}`;
  try {
    const client = await serverAdminClient();
    const [policyResponse, accountsResponse, quotaResponse] = await Promise.all([
      client.GET("/api/v1/budgets/{id}", { params: { path: { id: Number(id) }, query } }),
      client.GET("/api/v1/budgets/{id}/accounts", { params: { path: { id: Number(id) }, query } }),
      has(session, "quota.read") ? client.GET("/api/v1/quotas", { params: { query: { scope: quotaScope } } }) : Promise.resolve(null),
    ]);
    policy = unwrap(policyResponse);
    accounts = unwrap(accountsResponse).data ?? [];
    if (quotaResponse) quota = unwrap(quotaResponse);
  } catch (err) {
    const outcome = await handleAdminError(err);
    return <div className="mx-auto max-w-5xl p-8"><ForbiddenNotice message={outcome.message} /></div>;
  }
  const time = (value: string) => format.dateTime(new Date(value), { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" });
    const money = (value: number, currency: string) => `${microToDisplay(value)} ${currency?.toUpperCase() || t("unknownCurrency")}`;
    return <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <div><Button href={back} variant="outline" size="sm"><ArrowLeft className="h-3.5 w-3.5" />{t("back")}</Button></div>
      <div><h1 className="text-xl font-semibold text-foreground">{policy.name}</h1><p className="mt-1 text-sm text-muted-foreground">{t("accountsSubtitle")}</p></div>
      <Card className="grid grid-cols-2 gap-x-6 gap-y-4 p-4">
        <DetailField label={t("fields.scope_kind")}>{t(`scope.${policy.scope_kind}`)} {policy.scope_ref} {policy.environment}</DetailField>
        <DetailField label={t("fields.period")}>{t(`period.${policy.period}`)} · {policy.timezone}</DetailField>
        <DetailField label={t("fields.limit")}>{money(policy.limit, policy.currency)}</DetailField>
        <DetailField label={t("fields.mode")}>{t(`mode.${policy.mode}`)}</DetailField>
        <DetailField label={t("fields.thresholds")}>{policy.thresholds.map((value) => `${value}%`).join(", ") || t("none")}</DetailField>
        <DetailField label={t("fields.enabled")}><Badge variant={policy.enabled ? "success" : "outline"}>{t(`enabled.${policy.enabled}`)}</Badge></DetailField>
        <DetailField label={t("fields.createdAt")}>{time(policy.created_at)}</DetailField>
        <DetailField label={t("fields.version")}>{policy.version}</DetailField>
      </Card>
      {quota && <Card className="p-4"><DetailField label={t("legacyBalance")}>{quota.currency ? money(quota.balance, quota.currency) : t("legacyUnknown")}</DetailField><p className="mt-1 text-xs text-muted-foreground">{quota.scope}</p></Card>}
      <p className="text-sm text-muted-foreground">{t("enforceNote")}</p>
      <p className="text-xs text-muted-foreground">{t("accountingNote")} {t("quotaNote")}</p>
      <p className="text-xs text-muted-foreground">{t("reservedNote")}</p>
      <div className="overflow-x-auto rounded-lg border border-border bg-background">
        <table className="w-full border-collapse text-sm">
          <thead><tr className="border-b border-border bg-muted text-left">{["account", "periodWindow", "limit", "committed", "reserved", "released", "available"].map((field) => <th key={field} className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t(`fields.${field}`)}</th>)}</tr></thead>
          <tbody>{accounts.length === 0 ? <tr><td colSpan={7}><EmptyState icon={<ClipboardList className="h-6 w-6" />} title={t("emptyAccounts")} description={t("emptyAccountsHint")} /></td></tr> : accounts.map((account) => <tr key={account.id} className="border-b border-border last:border-b-0 transition-colors hover:bg-accent/50">
            <td className="px-4 py-2.5">{account.id}</td><td className="px-4 py-2.5">{time(account.period_start)}<div className="text-xs text-muted-foreground">{time(account.period_end)}</div></td>
            {(["limit", "committed", "reserved", "released", "available"] as const).map((field) => <td key={field} className={`px-4 py-2.5 ${field === "available" && account.available < 0 ? "text-destructive" : "text-foreground"}`}>{money(account[field], account.currency)}</td>)}
          </tr>)}</tbody>
        </table>
      </div>
      <div className="flex gap-2"><Button href={`${back}${back.includes("?") ? "&" : "?"}view=reservations`} variant="outline" size="sm">{t("views.reservations")}</Button><Button href={`${back}${back.includes("?") ? "&" : "?"}view=events`} variant="outline" size="sm">{t("views.events")}</Button></div>
    </div>;
}

