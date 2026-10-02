"use client";

import { useState, type ReactNode } from "react";
import { useFormatter, useTranslations } from "next-intl";
import { useSearchParams } from "next/navigation";
import { ClipboardList } from "lucide-react";
import { useRouter } from "@/i18n/navigation";
import { Button, Card, DetailField, Input } from "@/components/ui";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { Select } from "@/components/ui/select";
import { Modal } from "@/components/modal";
import { microToDisplay } from "@/lib/money";
import { BudgetForm } from "./form";
import { ResolveForm } from "./resolve-form";
import type { BudgetPolicy, BudgetEvent, Reservation, BudgetView } from "./types";

const cell = "px-4 py-2.5";
const rowClass = "border-b border-border last:border-b-0 transition-colors hover:bg-accent/50";

function BudgetTable({ headers, empty, emptyTitle, children }: { headers: string[]; empty: boolean; emptyTitle: string; children: ReactNode }) {
  return <div className="overflow-x-auto rounded-lg border border-border bg-background"><table className="w-full border-collapse text-sm"><thead><tr className="border-b border-border bg-muted text-left">{headers.map((header) => <th key={header} className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{header}</th>)}</tr></thead><tbody>{empty ? <tr><td colSpan={headers.length}><EmptyState icon={<ClipboardList className="h-6 w-6" />} title={emptyTitle} /></td></tr> : children}</tbody></table></div>;
}

export function BudgetsClient({ tenant, global, tenants, view, status, policies, events, reservations, nextCursor, canWrite, canResolve }: {
  tenant: string; global: boolean; tenants: string[]; view: BudgetView; status: string;
  policies: BudgetPolicy[]; events: BudgetEvent[]; reservations: Reservation[];
  nextCursor: string; canWrite: boolean; canResolve: boolean;
}) {
  const t = useTranslations("budgets");
  const tc = useTranslations("common");
  const format = useFormatter();
  const router = useRouter();
  const searchParams = useSearchParams();
  const [createOpen, setCreateOpen] = useState(false);
  const [edit, setEdit] = useState<BudgetPolicy | null>(null);
  const [resolve, setResolve] = useState<Reservation | null>(null);
  const [eventDetail, setEventDetail] = useState<BudgetEvent | null>(null);
  const [tenantInput, setTenantInput] = useState(tenant);

  function navigate(changes: Record<string, string>) {
    const query = new URLSearchParams(searchParams.toString());
    query.delete("cursor");
    for (const [key, value] of Object.entries(changes)) {
      if (value) query.set(key, value); else query.delete(key);
    }
    router.push(`/budgets?${query.toString()}`);
  }
  function money(value: number, currency?: string) {
    return `${microToDisplay(value)} ${currency?.toUpperCase() || t("unknownCurrency")}`;
  }
  function time(value: string) {
    return format.dateTime(new Date(value), { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" });
  }
  const columns = (keys: string[]) => keys.map((key) => t(`fields.${key}`));
  const selected = !global || !!tenant;
  const tenantOptions = Array.from(new Set([...tenants, ...(tenant ? [tenant] : [])]));

  return (
    <>
      <div className="flex items-center justify-between gap-4">
        <div><h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1><p className="mt-1 text-sm text-muted-foreground">{t("subtitle")}</p></div>
        {canWrite && selected && <Button onClick={() => setCreateOpen(true)}>{t("actions.create")}</Button>}
      </div>
      <Card className="flex flex-col gap-2 p-4">
        <p className="text-sm text-foreground">{t("enforceNote")}</p>
        <p className="text-xs text-muted-foreground">{t("accountingNote")}</p>
        <p className="text-xs text-muted-foreground">{t("quotaNote")}</p>
      </Card>
      {global ? <form className="flex flex-wrap items-end gap-3" onSubmit={(event) => { event.preventDefault(); navigate({ tenant: tenantInput.trim() }); }}>
        {tenantOptions.length > 0 && <label className="flex flex-col gap-1 text-sm text-foreground">{t("fields.tenant")}<Select name="tenant_selection" options={tenantOptions.map((value) => ({ value, label: value }))} value={tenant} onValueChange={(value) => { setTenantInput(value); navigate({ tenant: value }); }} searchable placeholder={tc("actions.select")} /></label>}
        <Input name="tenant" label={t("tenantName")} value={tenantInput} onChange={(event) => setTenantInput(event.target.value)} />
        <Button type="submit" variant="outline">{t("actions.apply")}</Button>
      </form> : <p className="text-sm text-muted-foreground">{t("tenantScope", { tenant })}</p>}
      <nav aria-label={t("viewsLabel")} className="flex flex-wrap gap-2">
        {(["policies", "events", "reservations"] as const).map((value) => <Button key={value} variant="outline" className={view === value ? "bg-accent text-accent-foreground" : ""} aria-current={view === value ? "page" : undefined} size="sm" onClick={() => navigate({ view: value })}>{t(`views.${value}`)}</Button>)}
      </nav>
      {!selected ? <EmptyState title={t("selectTenant")} description={t("selectTenantHint")} /> : <>
        {view === "policies" && <BudgetTable headers={columns(["name", "scope_kind", "period", "limit", "mode", "enabled", "actions"])} empty={policies.length === 0} emptyTitle={t("emptyPolicies")}>
          {policies.map((policy) => <tr key={policy.id} className={rowClass}>
            <td className={cell}>{policy.name}</td>
            <td className={cell}>{t(`scope.${policy.scope_kind}`)}{policy.scope_ref && <div className="text-xs text-muted-foreground">{policy.scope_ref} {policy.environment}</div>}</td>
            <td className={cell}>{t(`period.${policy.period}`)}<div className="text-xs text-muted-foreground">{policy.timezone}</div></td>
            <td className={cell}>{money(policy.limit, policy.currency)}</td>
            <td className={cell}><Badge variant={policy.mode === "enforce" ? "info" : "outline"}>{t(`mode.${policy.mode}`)}</Badge></td>
            <td className={cell}><Badge variant={policy.enabled ? "success" : "outline"}>{t(`enabled.${policy.enabled}`)}</Badge></td>
            <td className={cell}><div className="flex items-center justify-end gap-1"><Button href={`/budgets/${policy.id}${global ? `?tenant=${encodeURIComponent(tenant)}` : ""}`} size="sm" variant="outline">{t("actions.accounts")}</Button>{canWrite && <Button size="sm" variant="outline" onClick={() => setEdit(policy)}>{tc("actions.edit")}</Button>}</div></td>
          </tr>)}
        </BudgetTable>}
        {view === "events" && <>
          <p className="text-xs text-muted-foreground">{t("eventNote")}</p>
          <BudgetTable headers={columns(["time", "kind", "account", "thresholds", "amount", "actions"])} empty={events.length === 0} emptyTitle={t("emptyEvents")}>
            {events.map((event) => <tr key={event.id} className={rowClass}>
              <td className={cell}>{time(event.created_at)}</td><td className={cell}>{t.has(`eventKind.${event.kind}`) ? t(`eventKind.${event.kind}`) : event.kind}</td><td className={cell}>{event.account_id ?? t("none")}</td><td className={cell}>{event.threshold ? `${event.threshold}%` : t("none")}</td><td className={cell}>{money(event.amount)}</td><td className={cell}><Button size="sm" variant="outline" onClick={() => setEventDetail(event)}>{t("actions.details")}</Button></td>
            </tr>)}
          </BudgetTable>
        </>}
        {view === "reservations" && <>
          <p className="text-sm text-muted-foreground">{t("unknownNote")}</p>
          <label className="flex max-w-xs flex-col gap-1 text-sm text-foreground">{t("fields.status")}<Select name="status" value={status} onValueChange={(value) => navigate({ status: value })} options={["unknown", "released_unknown", "reserved", "dispatched", "settled", "released", "all"].map((value) => ({ value, label: t(`status.${value}`) }))} placeholder={tc("actions.select")} /></label>
          <BudgetTable headers={columns(["request", "status", "estimate", "actual", "reason", "actions"])} empty={reservations.length === 0} emptyTitle={t("emptyReservations")}>
            {reservations.map((reservation) => <tr key={reservation.id} className={rowClass}>
              <td className={`${cell} break-all`}>{reservation.request_id}<div className="text-xs text-muted-foreground">{time(reservation.created_at)}</div></td>
              <td className={cell}><Badge variant={reservation.status === "unknown" || reservation.status === "released_unknown" ? "warning" : "outline"}>{t(`status.${reservation.status}`)}</Badge></td>
              <td className={cell}>{money(reservation.estimate, reservation.currency)}</td><td className={cell}>{reservation.actual == null ? t("unknownAmount") : money(reservation.actual, reservation.currency)}</td>
              <td className={`${cell} max-w-xs break-words`}>{reservation.reason || t("none")}</td>
              <td className={cell}>{canResolve && (reservation.status === "unknown" || reservation.status === "released_unknown") && <Button size="sm" variant="outline" onClick={() => setResolve(reservation)}>{t("actions.resolve")}</Button>}</td>
            </tr>)}
          </BudgetTable>
        </>}
        <div className="flex justify-end gap-2">
          {searchParams.has("cursor") && <Button variant="outline" size="sm" onClick={() => navigate({ cursor: "" })}>{tc("pagination.firstPage")}</Button>}
          {nextCursor && <Button variant="outline" size="sm" onClick={() => navigate({ cursor: nextCursor })}>{tc("actions.nextPage")}</Button>}
          <Button variant="outline" size="sm" onClick={() => router.refresh()}>{t("actions.refresh")}</Button>
        </div>
      </>}
      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title={t("actions.create")} size="xl">{createOpen && <BudgetForm tenant={tenant} onClose={() => setCreateOpen(false)} />}</Modal>
      <Modal open={edit !== null} onClose={() => setEdit(null)} title={t("actions.edit")} size="lg">{edit && <BudgetForm key={edit.id} tenant={tenant} policy={edit} onClose={() => setEdit(null)} />}</Modal>
      <Modal open={resolve !== null} onClose={() => setResolve(null)} title={t("actions.resolve")} size="lg">{resolve && <ResolveForm key={resolve.id} tenant={tenant} reservation={resolve} onClose={() => setResolve(null)} />}</Modal>
      <Modal open={eventDetail !== null} onClose={() => setEventDetail(null)} title={t("actions.details")} size="lg">{eventDetail && <div className="flex flex-col gap-4"><DetailField label={t("fields.reservation")}>{eventDetail.reservation_id || t("none")}</DetailField><DetailField label={t("fields.operator")}>{eventDetail.operator_id ?? t("none")}</DetailField><DetailField label={t("fields.reason")}>{eventDetail.reason || t("none")}</DetailField><DetailField label={t("fields.evidence")}>{eventDetail.evidence || t("none")}</DetailField></div>}</Modal>
    </>
  );
}
