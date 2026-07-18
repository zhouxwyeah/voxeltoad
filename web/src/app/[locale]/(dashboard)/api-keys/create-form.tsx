"use client";

import { useActionState, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createAPIKey, updateAPIKey } from "./actions";
import { Button, Input } from "@/components/ui";
import { MultiSelect } from "@/components/multi-select";

type ModelOption = { value: string; label: string };

export function APIKeyForm({
  models,
  defaultValues,
  onCancel,
  onSuccess,
}: {
  models: ModelOption[];
  defaultValues?: Record<string, unknown> | null;
  onCancel?: () => void;
  onSuccess?: (plaintext?: string) => void;
}) {
  const isEdit = !!defaultValues;
  const t = useTranslations("api-keys");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(
    isEdit ? updateAPIKey : createAPIKey,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  const dvModels = useMemo(
    () => (defaultValues?.allowed_models as string[] | undefined) ?? [],
    [defaultValues],
  );
  const [selectedModels, setSelectedModels] = useState<string[]>(dvModels);

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSelectedModels(isEdit ? dvModels : []);
      const plaintext = isEdit
        ? undefined
        : (state as { ok: true; apiKey?: string }).apiKey;
      onSuccessRef.current?.(plaintext);
      router.refresh();
    }
  }, [state, router, isEdit, dvModels]);

  return (
    <form ref={formRef} action={formAction} className="flex flex-col gap-4">
      {/* key_id: editable for create, disabled for edit */}
      {isEdit && (
        <input
          type="hidden"
          name="key_id"
          value={String(defaultValues?.key_id ?? "")}
        />
      )}
      <Input
        name="key_id"
        label={t("form.keyId.label")}
        placeholder={t("form.keyId.placeholder")}
        required={!isEdit}
        defaultValue={String(defaultValues?.key_id ?? "")}
        disabled={isEdit}
      />
      {models.length > 0 ? (
        <MultiSelect
          name="allowed_models"
          options={models}
          value={selectedModels}
          onChange={setSelectedModels}
          label={t("form.allowedModels.label")}
          placeholder={t("form.allowedModels.placeholder")}
          selectAllLabel={t("form.allowedModels.selectAll")}
        />
      ) : (
        <p className="text-sm text-muted-foreground">
          {t("form.allowedModels.empty")}
        </p>
      )}
      {state && !state.ok && (
        <p
          role="alert"
          className="w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {state.errorKey ? tErr(state.errorKey) : state.error}
        </p>
      )}
      <div className="flex justify-end gap-3 pt-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          {tCommon("actions.cancel")}
        </Button>
        <Button type="submit" disabled={pending}>
          {pending
            ? tCommon("actions.saving")
            : isEdit
              ? t("actions.save")
              : t("actions.create")}
        </Button>
      </div>
    </form>
  );
}
