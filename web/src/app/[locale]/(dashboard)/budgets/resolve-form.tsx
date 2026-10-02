"use client";

import { useState, useTransition } from "react";
import { useTranslations } from "next-intl";
import { useRouter } from "@/i18n/navigation";
import { Button, DetailField, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmModal, modalFormActionsClass } from "@/components/modal";
import { microToDisplay } from "@/lib/money";
import { toast } from "@/lib/toast";
import { resolveReservation } from "./actions";
import type { Reservation } from "./types";

export function ResolveForm({ tenant, reservation, onClose }: { tenant: string; reservation: Reservation; onClose: () => void }) {
  const t = useTranslations("budgets");
  const tc = useTranslations("common");
  const te = useTranslations("errors");
  const router = useRouter();
  const [action, setAction] = useState("settle");
  const [confirmation, setConfirmation] = useState<FormData | null>(null);
  const [pending, startTransition] = useTransition();
  const [error, setError] = useState("");
  const actions = reservation.status === "released_unknown" ? ["settle"] : ["settle", "release", "release_unknown"];

  function resolve() {
    if (!confirmation) return;
    setError("");
    startTransition(async () => {
      const result = await resolveReservation(tenant, reservation.id, reservation.version, confirmation);
      if (!result.ok) {
        setError(result.errorKey ? te(result.errorKey) : result.error);
        return;
      }
      toast.success(t("resolved"));
      router.refresh();
      onClose();
    });
  }

  return (
    <>
      <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); setConfirmation(new FormData(event.currentTarget)); }}>
        <DetailField label={t("fields.request")}>{reservation.request_id}</DetailField>
        <DetailField label={t("fields.estimate")}>{microToDisplay(reservation.estimate)} {reservation.currency?.toUpperCase() || t("unknownCurrency")}</DetailField>
        <DetailField label={t("fields.reason")}>{reservation.reason || t("none")}</DetailField>
        <p className="text-sm text-muted-foreground">{t("unknownNote")}</p>
        <label className="flex flex-col gap-1 text-sm text-foreground">
          {t("fields.resolution")}
          <Select name="action" value={action} onValueChange={setAction} options={actions.map((value) => ({ value, label: t(`resolution.${value}`) }))} placeholder={tc("actions.select")} />
        </label>
        {action === "settle" && <><Input name="actual" label={t("fields.actual")} inputMode="decimal" required /><p className="text-xs text-muted-foreground">{t("actualHint")}</p></>}
        <Input name="reason" label={t("fields.reason")} required />
        <label className="flex flex-col gap-1 text-sm text-foreground">
          {t("fields.evidence")}
          <Textarea name="evidence" required />
        </label>
        <p className="text-xs text-muted-foreground">{t("evidenceHint")}</p>
        <div className={modalFormActionsClass}>
          <Button type="button" variant="outline" disabled={pending} onClick={onClose}>{tc("actions.cancel")}</Button>
          <Button type="submit" disabled={pending}>{t("actions.review")}</Button>
        </div>
      </form>
      <ConfirmModal open={confirmation !== null} onCancel={() => { if (!pending) { setConfirmation(null); setError(""); } }} onConfirm={resolve} title={t(action === "release_unknown" ? "riskTitle" : "confirmResolve")} message={t(action === "release_unknown" ? "riskNote" : action === "release" ? "releaseNote" : "settleNote")} confirmLabel={t(`resolution.${action}`)} loadingLabel={tc("actions.saving")} loading={pending} error={error} />
    </>
  );
}
