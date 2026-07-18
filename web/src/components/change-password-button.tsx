"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useTranslations } from "next-intl";
import { changePassword } from "@/app/[locale]/(dashboard)/actions";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";

export function ChangePasswordButton() {
  const t = useTranslations("common");
  const tErr = useTranslations("errors");
  const [open, setOpen] = useState(false);
  const [state, formAction, pending] = useActionState(changePassword, null);
  const [success, setSuccess] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  async function handleSubmit(formData: FormData) {
    formAction(formData);
  }

  // Show success message + auto-close modal after 2s.
  useEffect(() => {
    if (state?.ok && !success) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSuccess(true);
      timerRef.current = setTimeout(() => {
        setOpen(false);
        setSuccess(false);
      }, 2000);
    }
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [state, success]);

  function handleClose() {
    if (timerRef.current) clearTimeout(timerRef.current);
    setOpen(false);
    setSuccess(false);
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-md px-3 py-1.5 text-left text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        {t("changePassword.button")}
      </button>

      <Modal
        open={open}
        onClose={handleClose}
        title={t("changePassword.title")}
        size="sm"
      >
        <form action={handleSubmit} className="flex flex-col gap-4">
          <label className="flex flex-col gap-1 text-sm">
            <span className="font-medium text-foreground">
              {t("changePassword.newPassword")}
            </span>
            <input
              name="password"
              type="password"
              required
              className="h-9 rounded-md border border-input bg-background px-3 text-sm text-foreground placeholder:text-muted-foreground/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0"
            />
          </label>

          {state && !state.ok && (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {state.errorKey ? tErr(state.errorKey) : state.error}
            </p>
          )}
          {success && (
            <p className="rounded-md bg-primary/10 px-3 py-2 text-sm text-primary">
              {t("changePassword.success")}
            </p>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <Button type="button" variant="outline" onClick={handleClose}>
              {t("actions.cancel")}
            </Button>
            <Button type="submit" disabled={pending}>
              {pending ? t("actions.saving") : t("actions.save")}
            </Button>
          </div>
        </form>
      </Modal>
    </>
  );
}
