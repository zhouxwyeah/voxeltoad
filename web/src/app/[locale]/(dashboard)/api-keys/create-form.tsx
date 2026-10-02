"use client";

import { useActionState, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createAPIKey, updateAPIKey } from "./actions";
import { Button, Input } from "@/components/ui";
import { MultiSelect } from "@/components/multi-select";
import { Select } from "@/components/ui/select";
import { modalFormActionsClass } from "@/components/modal";
import { toast } from "@/lib/toast";

type ModelOption = { value: string; label: string };

export function APIKeyForm({
  models,
  groups,
  applications,
  defaultValues,
  onCancel,
  onSuccess,
}: {
  models: ModelOption[];
  groups: ModelOption[];
  applications: ModelOption[];
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
  const [groupId, setGroupId] = useState(String(defaultValues?.group_id ?? ""));
  const [applicationId, setApplicationId] = useState(String(defaultValues?.application_id ?? ""));
  const [environment, setEnvironment] = useState(String(defaultValues?.environment ?? ""));
  const bound = !!(defaultValues?.group_id && defaultValues?.application_id && defaultValues?.environment);
  const identityFields = [
    { name: "group_id", label: t("identity.group"), value: groupId, set: setGroupId, options: groups },
    { name: "application_id", label: t("identity.application"), value: applicationId, set: setApplicationId, options: applications },
    { name: "environment", label: t("identity.environment"), value: environment, set: setEnvironment, options: ["dev", "staging", "prod"].map((value) => ({ value, label: value })) },
  ];

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSelectedModels(isEdit ? dvModels : []);
      const plaintext = isEdit
        ? undefined
        : (state as { ok: true; apiKey?: string }).apiKey;
      if (isEdit) toast.success(t("actions.saved"));
      onSuccessRef.current?.(plaintext);
      router.refresh();
    }
  }, [state, router, isEdit, dvModels, t]);

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
      {isEdit && <input type="hidden" name="had_models" value={String(dvModels.length > 0)} />}
      {isEdit && !bound && <input type="hidden" name="bind_identity" value="true" />}
      <p className="text-sm text-muted-foreground">{t(bound ? "identity.immutable" : "identity.bindingHint")}</p>
      {identityFields.map((field) => (
        <label key={field.name} className="flex flex-col gap-1.5">
          <span className="text-sm font-medium text-foreground">{field.label} *</span>
          {defaultValues?.[field.name] ? (
            <>
              {!bound && <input type="hidden" name={field.name} value={field.value} />}
              <span className="text-sm text-muted-foreground">{field.options.find((option) => option.value === field.value)?.label ?? field.value}</span>
            </>
          ) : (
            <Select name={field.name} options={field.options} value={field.value} onValueChange={field.set} placeholder={tCommon("actions.select")} searchable={field.name !== "environment"} className="h-9 w-full" />
          )}
        </label>
      ))}
      {!groupId && groups.length === 0 && <Button href="/groups" variant="outline">{t("identity.createGroup")}</Button>}
      {!applicationId && applications.length === 0 && <Button href="/applications" variant="outline">{t("identity.createApplication")}</Button>}
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
      <div className={modalFormActionsClass}>
        <Button type="button" variant="outline" onClick={onCancel}>
          {tCommon("actions.cancel")}
        </Button>
        <Button type="submit" disabled={pending || !groupId || !applicationId || !environment}>
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
