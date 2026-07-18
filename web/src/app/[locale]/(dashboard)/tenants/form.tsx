"use client";

import { useActionState, useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createTenant } from "./actions";
import { Button, Input } from "@/components/ui";

/**
 * Tenant create form. Tenant names are immutable (no PUT/PATCH for name), so
 * unlike providers there is no "edit" mode here — only create. Enabling/
 * disabling is a separate action in the table (table.tsx), not this form.
 */
export function TenantForm({
  onCancel,
  onSuccess,
}: {
  onCancel?: () => void;
  onSuccess?: () => void;
}) {
  const t = useTranslations("tenants");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(createTenant, null);
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      onSuccessRef.current?.();
      router.refresh();
    }
  }, [state, router]);

  return (
    <form ref={formRef} action={formAction} className="flex flex-col gap-4">
      <Input name="name" label={t("form.name.label")} required />
      {state && !state.ok && (
        <p
          role="alert"
          className="w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {state.errorKey ? tErr(state.errorKey) : state.error}
        </p>
      )}
      <div className="flex justify-end gap-3 pt-2">
        <Button type="button" variant="outline" onClick={onCancel ?? onSuccess}>
          {tCommon("actions.cancel")}
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? tCommon("actions.saving") : t("actions.create")}
        </Button>
      </div>
    </form>
  );
}
