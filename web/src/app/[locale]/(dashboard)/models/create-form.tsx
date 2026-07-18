"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createModel, updateModel } from "./actions";
import { Button, Input } from "@/components/ui";
import { Textarea } from "@/components/ui/textarea";
import { UpstreamRow } from "./upstream-row";

type ProviderOption = { name: string };
type Pricing = {
  prompt_per_1m?: number;
  completion_per_1m?: number;
  currency?: string;
  cache_hit_multiplier?: number;
};
type ModelUpstream = {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing?: Pricing;
};
type ModelRow = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

/**
 * Model create/edit form (POST upsert). Used inside a Modal.  In edit mode
 * the alias is locked (identifier cannot change) and upstream rows are
 * pre-filled from the existing model config.
 */
export function ModelForm({
  providers,
  defaultValues,
  onSuccess,
  onCancel,
}: {
  providers: ProviderOption[];
  defaultValues?: ModelRow | null;
  onSuccess?: () => void;
  onCancel?: () => void;
}) {
  const isEdit = !!defaultValues;
  const t = useTranslations("models");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(
    isEdit ? updateModel : createModel,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  const upstreamDefaults = defaultValues?.upstreams ?? [];
  const [rows, setRows] = useState<{ key: string; idx: number }[]>(
    upstreamDefaults.map((_, i) => ({ key: crypto.randomUUID(), idx: i })),
  );
  // Parallel array of provider names per row, kept in sync with `rows`.
  // Lifted to parent because <Select> is controlled-only; its hidden input
  // keeps name="upstream_provider" so the getAll() zip order is preserved.
  const [providerValues, setProviderValues] = useState<string[]>(
    upstreamDefaults.map((u) => u.provider ?? ""),
  );

  function addRow() {
    setRows((r) => [...r, { key: crypto.randomUUID(), idx: -1 }]);
    setProviderValues((v) => [...v, ""]);
  }
  function removeRow(key: string) {
    const i = rows.findIndex((r) => r.key === key);
    if (i < 0) return;
    setRows((r) => r.filter((row) => row.key !== key));
    setProviderValues((v) => v.filter((_, idx) => idx !== i));
  }

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      // Controlled <Select> values are not cleared by form.reset(); reset them
      // explicitly so the form does not retain stale provider selections.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setProviderValues([]);
      onSuccessRef.current?.();
      router.refresh();
    }
  }, [state, router]);

  return (
    <form ref={formRef} action={formAction} className="flex flex-col gap-4">
      <Input
        name="alias"
        label={t("form.alias.label")}
        placeholder={t("form.alias.placeholder")}
        required
        defaultValue={defaultValues?.alias ?? ""}
        disabled={isEdit}
      />
      {isEdit && (
        <input type="hidden" name="alias" value={String(defaultValues?.alias ?? "")} />
      )}

      <label className="flex flex-col gap-1 text-sm">
        <span className="font-medium text-foreground">
          {t("form.description.label")}
        </span>
        <Textarea
          name="description"
          placeholder={t("form.description.placeholder")}
          defaultValue={defaultValues?.description ?? ""}
          rows={3}
        />
      </label>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input
          name="context_length"
          type="number"
          label={t("form.contextLength.label")}
          placeholder={t("form.contextLength.placeholder")}
          defaultValue={
            defaultValues?.context_length
              ? String(defaultValues.context_length)
              : ""
          }
          min={0}
        />
        <Input
          name="capabilities"
          label={t("form.capabilities.label")}
          placeholder={t("form.capabilities.placeholder")}
          defaultValue={(defaultValues?.capabilities ?? []).join(", ")}
        />
      </div>

      <Input
        name="tags"
        label={t("form.tags.label")}
        placeholder={t("form.tags.placeholder")}
        defaultValue={(defaultValues?.tags ?? []).join(", ")}
      />

      <div className="flex flex-col gap-3">
        <span className="text-sm font-medium text-foreground">
          {t("columns.upstreams")}
        </span>
        {rows.map((row, index) => (
          <UpstreamRow
            key={row.key}
            providers={providers}
            defaultValues={row.idx >= 0 ? upstreamDefaults[row.idx] : undefined}
            providerValue={providerValues[index] ?? ""}
            onProviderChange={(v) =>
              setProviderValues((arr) =>
                arr.map((x, idx) => (idx === index ? v : x)),
              )
            }
            onRemove={() => removeRow(row.key)}
          />
        ))}
        <Button type="button" variant="outline" size="sm" onClick={addRow}>
          {t("form.upstreams.add")}
        </Button>
      </div>

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
            ? t("actions.saving")
            : t(isEdit ? "actions.save" : "actions.create")}
        </Button>
      </div>
    </form>
  );
}
