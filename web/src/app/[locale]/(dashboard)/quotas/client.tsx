"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { microToDisplay } from "@/lib/money";
import { topupQuota } from "./actions";

export function QuotasPageClient({
  scope,
  balance,
  currency,
  fetchError,
}: {
  scope: string;
  balance: number;
  currency: string;
  fetchError: string;
}) {
  const t = useTranslations("quotas");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const router = useRouter();
  const [topupOpen, setTopupOpen] = useState(false);
  const [state, formAction, pending] = useActionState(topupQuota, null);
  const formRef = useRef<HTMLFormElement>(null);

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setTopupOpen(false);
      router.refresh();
    }
  }, [state, router]);

  const inputClass =
    "h-9 rounded-md border border-input bg-background px-3 text-sm text-foreground placeholder:text-muted-foreground/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0";

  return (
    <>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">
            {t("heading")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("subtitle")}
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="primary" size="sm" onClick={() => setTopupOpen(true)}>
            {t("topup.button")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => router.refresh()}
          >
            {t("actions.refresh")}
          </Button>
        </div>
      </div>

      {fetchError && (
        <div className="rounded-md bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {fetchError}
        </div>
      )}

      {!scope ? (
        <div className="flex min-h-[200px] items-center justify-center rounded-lg border border-border bg-background">
          <p className="text-muted-foreground">
            {t("emptyState")}
          </p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border bg-background">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <th className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("scope")}
                </th>
                <th className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("balance")}
                </th>
                <th className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("currency")}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr className="transition-colors hover:bg-accent/50">
                <td className="px-4 py-3 text-foreground font-mono text-xs">
                  {scope}
                </td>
                <td className="px-4 py-3 text-foreground font-mono">
                  {microToDisplay(balance)}
                </td>
                <td className="px-4 py-3 text-foreground">
                  {currency || "—"}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      )}

      <Modal
        open={topupOpen}
        onClose={() => setTopupOpen(false)}
        title={t("topup.title")}
        size="sm"
      >
        <form ref={formRef} action={formAction} className="flex flex-col gap-4">
          <label className="flex flex-col gap-1 text-sm">
            <span className="font-medium text-foreground">
              {t("topup.scope.label")}
            </span>
            <input
              name="scope"
              defaultValue={scope}
              required
              className={inputClass}
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            <span className="font-medium text-foreground">
              {t("topup.amount.label")}
            </span>
            <input
              name="amount"
              type="number"
              min="1"
              step="0.01"
              required
              className={inputClass}
              placeholder={t("topup.amount.placeholder")}
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            <span className="font-medium text-foreground">
              {t("topup.currency.label")}
            </span>
            <input
              name="currency"
              defaultValue={currency || "USD"}
              className={inputClass}
              placeholder="USD"
            />
          </label>

          {state && !state.ok && (
            <p
              role="alert"
              className="w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {state.errorKey ? tErr(state.errorKey) : state.error}
            </p>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => setTopupOpen(false)}
            >
              {tCommon("actions.cancel")}
            </Button>
            <Button type="submit" disabled={pending}>
              {pending ? tCommon("actions.saving") : t("topup.button")}
            </Button>
          </div>
        </form>
      </Modal>
    </>
  );
}
