"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createApplication } from "./actions";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";
import { modalFormActionsClass } from "@/components/modal";

type GroupRow = Record<string, unknown>;

export function ApplicationForm({
  groups,
  onCancel,
  onSuccess,
}: {
  groups: GroupRow[];
  onCancel?: () => void;
  onSuccess?: () => void;
}) {
  const t = useTranslations("applications");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(createApplication, null);
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  const [ownerGroup, setOwnerGroup] = useState("");
  const groupOptions = groups.map((g) => {
    const name = String(g.name ?? "");
    return { value: name, label: name };
  });

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
      <label className="flex flex-col gap-1.5">
        <span className="text-sm font-medium text-foreground">
          {t("form.ownerGroup.label")}
        </span>
        <Select
          name="owner_group"
          options={groupOptions}
          value={ownerGroup}
          onValueChange={setOwnerGroup}
          placeholder={tCommon("actions.select")}
          className="h-9 w-full"
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
      <div className={modalFormActionsClass}>
        <Button type="button" variant="outline" onClick={onCancel}>
          {tCommon("actions.cancel")}
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? tCommon("actions.saving") : t("actions.save")}
        </Button>
      </div>
    </form>
  );
}
