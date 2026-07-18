"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { upsertPlugin, updatePlugin } from "./actions";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";

type PluginRow = {
  name: string;
  phase?: "pre" | "post";
  enabled?: boolean;
  scope?: string;
  params?: Record<string, unknown>;
};

/**
 * Plugin create/edit form. Used inside a Modal for both create and edit
 * (POST upsert with defaultValue pre-fill).
 *
 * Key behaviors:
 * - name is disabled when editing (identifier can't change) + hidden field
 * - params edited as JSON textarea with client-side validation
 * - enabled as checkbox
 */
export function PluginForm({
  defaultValues,
  onSuccess,
  onCancel,
}: {
  defaultValues?: PluginRow | null;
  onSuccess?: () => void;
  onCancel?: () => void;
}) {
  const isEdit = !!defaultValues;
  const t = useTranslations("plugins");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(
    isEdit ? updatePlugin : upsertPlugin,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);

  useEffect(() => {
    onSuccessRef.current = onSuccess;
  }, [onSuccess]);

  const [paramsText, setParamsText] = useState(
    isEdit && defaultValues?.params
      ? JSON.stringify(defaultValues.params, null, 2)
      : "",
  );
  const [paramsError, setParamsError] = useState<string | null>(null);
  const [phase, setPhase] = useState(defaultValues?.phase ?? "");

  function handleParamsChange(value: string) {
    setParamsText(value);
    if (!value.trim()) {
      setParamsError(null);
      return;
    }
    try {
      JSON.parse(value);
      setParamsError(null);
    } catch (e) {
      setParamsError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      onSuccessRef.current?.();
      router.refresh();
    }
  }, [state, router]);

  return (
    <form ref={formRef} action={formAction} className="flex flex-col gap-4">
      {/* Name: disabled in edit mode + hidden field to submit the value */}
      {isEdit ? (
        <>
          <Input
            label={t("form.name.label")}
            defaultValue={defaultValues.name}
            disabled
          />
          <input type="hidden" name="name" value={defaultValues.name} />
        </>
      ) : (
        <Input
          name="name"
          label={t("form.name.label")}
          placeholder={t("form.name.placeholder")}
          required
        />
      )}

      {/* Phase: select pre/post */}
      <div className="flex flex-col gap-1.5">
        <label className="text-sm font-medium text-foreground">
          {t("form.phase.label")}
        </label>
        <Select
          name="phase"
          value={phase}
          onValueChange={setPhase}
          placeholder={t("form.phase.placeholder")}
          options={[
            { value: "pre", label: t("phase.pre") },
            { value: "post", label: t("phase.post") },
          ]}
          className="h-9"
        />
      </div>

      {/* Enabled: checkbox */}
      <div className="flex items-center gap-2">
        <input
          type="checkbox"
          name="enabled"
          id="plugin-enabled"
          value="true"
          defaultChecked={defaultValues?.enabled ?? false}
          className="h-4 w-4 rounded border-input text-primary focus:ring-2 focus:ring-ring"
        />
        <label htmlFor="plugin-enabled" className="text-sm text-foreground">
          {t("form.enabled.label")}
        </label>
      </div>

      {/* Scope */}
      <Input
        name="scope"
        label={t("form.scope.label")}
        placeholder={t("form.scope.placeholder")}
        defaultValue={defaultValues?.scope ?? ""}
      />

      {/* Params JSON editor */}
      <div className="flex flex-col gap-1.5">
        <label className="text-sm font-medium text-foreground">
          {t("form.params.label")}
        </label>
        <textarea
          name="params_text"
          rows={6}
          className="rounded-md border border-input bg-background px-3 py-2 text-sm font-mono focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          placeholder={t("form.params.placeholder")}
          value={paramsText}
          onChange={(e) => handleParamsChange(e.target.value)}
          onBlur={(e) => handleParamsChange(e.target.value)}
        />
        <input type="hidden" name="params_json" value={paramsText} />
        {paramsError && (
          <p className="text-xs text-destructive">
            {t("form.params.invalidJson", { error: paramsError })}
          </p>
        )}
      </div>

      {/* Server error */}
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
            : t(isEdit ? "actions.edit" : "actions.create")}
        </Button>
      </div>
    </form>
  );
}
