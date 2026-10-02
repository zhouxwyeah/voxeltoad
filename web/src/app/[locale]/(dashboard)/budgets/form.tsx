"use client";

import { useState, useTransition } from "react";
import { useTranslations } from "next-intl";
import { useRouter } from "@/i18n/navigation";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";
import { ConfirmModal, modalFormActionsClass } from "@/components/modal";
import { microToDisplay } from "@/lib/money";
import { toast } from "@/lib/toast";
import { createBudget, updateBudget } from "./actions";
import type { BudgetPolicy } from "./types";

export function BudgetForm({ tenant, policy, onClose }: { tenant: string; policy?: BudgetPolicy; onClose: () => void }) {
  const t = useTranslations("budgets");
  const tc = useTranslations("common");
  const te = useTranslations("errors");
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [error, setError] = useState("");
  const [scope, setScope] = useState("tenant");
  const [environment, setEnvironment] = useState("prod");
  const [period, setPeriod] = useState("monthly");
  const [currency, setCurrency] = useState("usd");
  const [mode, setMode] = useState("enforce");
  const [enabled, setEnabled] = useState(String(policy?.enabled ?? true));
  const [limit, setLimit] = useState(policy ? microToDisplay(policy.limit) : "");
  const [confirmation, setConfirmation] = useState<FormData | null>(null);
  const select = (name: string, value: string, setValue: (value: string) => void, values: string[], prefix: string) => (
    <label className="flex flex-col gap-1 text-sm text-foreground">
      {t(`fields.${name}`)}
      <Select name={name} value={value} onValueChange={setValue} options={values.map((item) => ({ value: item, label: t(`${prefix}.${item}`) }))} placeholder={tc("actions.select")} />
    </label>
  );

  function save(form: FormData) {
    setError("");
    startTransition(async () => {
      const result = policy ? await updateBudget(tenant, policy.id, policy.version, form) : await createBudget(tenant, form);
      if (!result.ok) {
        setError(result.errorKey ? te(result.errorKey) : result.error);
        return;
      }
      toast.success(t("saved"));
      router.refresh();
      onClose();
    });
  }

  return (
    <>
      <form onSubmit={(event) => {
        event.preventDefault();
        const form = new FormData(event.currentTarget);
        if (policy) setConfirmation(form); else save(form);
      }} className="flex flex-col gap-4">
        {policy ? (
          <>
            <p className="text-sm text-muted-foreground">{t("editNote")}</p>
            <p className="text-sm text-foreground">{policy.name} · {policy.currency.toUpperCase()}</p>
            {select("enabled", enabled, setEnabled, ["true", "false"], "enabled")}
            <input type="hidden" name="limitChanged" value={String(limit !== microToDisplay(policy.limit))} />
          </>
        ) : (
          <>
            <Input name="name" label={t("fields.name")} required />
            {select("scope_kind", scope, setScope, ["tenant", "group", "application", "application_env", "key"], "scope")}
            {scope !== "tenant" && <><Input key={scope} name="scope_ref" label={t("fields.scope_ref")} required /><p className="text-xs text-muted-foreground">{t(scope === "key" ? "keyRefHint" : "scopeRefHint")}</p></>}
            {scope === "application_env" && select("environment", environment, setEnvironment, ["dev", "staging", "prod"], "environment")}
            <div className="grid grid-cols-2 gap-4">
              {select("period", period, setPeriod, ["daily", "weekly", "monthly"], "period")}
              <Input name="timezone" label={t("fields.timezone")} defaultValue="UTC" required />
              {select("currency", currency, setCurrency, ["usd", "cny"], "currency")}
              {select("mode", mode, setMode, ["enforce", "soft"], "mode")}
            </div>
            <Input name="thresholds" label={t("fields.thresholds")} defaultValue="80,100" />
            <p className="text-xs text-muted-foreground">{t("thresholdHint")}</p>
            <p className="text-sm text-muted-foreground">{t("startsNow")}</p>
          </>
        )}
        <Input name="limit" label={t("fields.limit")} inputMode="decimal" value={limit} onChange={(event) => setLimit(event.target.value)} required />
        <p className="text-sm text-muted-foreground">{t("enforceNote")}</p>
        {error && !confirmation && <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}
        <div className={modalFormActionsClass}>
          <Button type="button" variant="outline" disabled={pending} onClick={onClose}>{tc("actions.cancel")}</Button>
          <Button type="submit" disabled={pending}>{pending ? tc("actions.saving") : tc("actions.save")}</Button>
        </div>
      </form>
      <ConfirmModal open={confirmation !== null} onCancel={() => { if (!pending) { setConfirmation(null); setError(""); } }} onConfirm={() => { if (confirmation) save(confirmation); }} title={t("confirmEdit")} message={t("editNote")} confirmLabel={tc("actions.save")} loadingLabel={tc("actions.saving")} loading={pending} error={error} />
    </>
  );
}
