"use client";

import { useActionState, useEffect, useRef, useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { upsertRoute, updateRoute } from "./actions";
import { Button, Input } from "@/components/ui";
import { modalFormActionsClass } from "@/components/modal";
import { Select } from "@/components/ui/select";
import { RouteProviderRow } from "./route-provider-row";

type RouteProvider = {
  name: string;
  weight?: number;
};

type RouteRow = {
  model_alias: string;
  providers?: RouteProvider[];
  strategy?: "priority" | "weighted" | "round_robin" | "session_affinity";
};

type ModelOption = {
  alias: string;
  upstreams?: { provider: string; upstream_model: string }[];
};

type ProviderOption = { name: string };

export function RouteForm({
  models,
  providers,
  defaultValues,
  onSuccess,
  onCancel,
}: {
  models: ModelOption[];
  providers: ProviderOption[];
  defaultValues?: RouteRow | null;
  onSuccess?: () => void;
  onCancel?: () => void;
}) {
  const t = useTranslations("routes");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const isEdit = !!defaultValues;
  const [state, formAction, pending] = useActionState(
    isEdit ? updateRoute : upsertRoute,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);

  useEffect(() => {
    onSuccessRef.current = onSuccess;
  }, [onSuccess]);

  const [rows, setRows] = useState<{ key: string }[]>(() =>
    (defaultValues?.providers ?? []).map(() => ({ key: crypto.randomUUID() })),
  );
  // Parallel array of provider names for each row, kept in sync with `rows`.
  // Lifted to parent because <Select> is controlled-only; the hidden input it
  // emits (name="route_provider_name") preserves the getAll() zip order.
  const [providerValues, setProviderValues] = useState<string[]>(
    (defaultValues?.providers ?? []).map((p) => p.name ?? ""),
  );

  const [selectedModel, setSelectedModel] = useState(
    defaultValues?.model_alias ?? "",
  );
  const [strategy, setStrategy] = useState(defaultValues?.strategy ?? "");

  // Filter providers to only those that are upstreams of the selected model
  const allowedProviders = useMemo(() => {
    if (!selectedModel) return providers;
    const model = models.find((m) => m.alias === selectedModel);
    if (!model?.upstreams) return providers;
    const upstreamNames = new Set(
      model.upstreams.map((u) => u.provider),
    );
    return providers.filter((p) => upstreamNames.has(p.name));
  }, [selectedModel, models, providers]);

  function addRow() {
    setRows((r) => [...r, { key: crypto.randomUUID() }]);
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

  const strategies = [
    "priority",
    "weighted",
    "round_robin",
    "session_affinity",
  ] as const;

  return (
    <form ref={formRef} action={formAction} className="flex flex-col gap-4">
      {/* Model alias */}
      {isEdit ? (
        <>
          <Input
            label={t("form.modelAlias.label")}
            value={defaultValues.model_alias}
            disabled
          />
          <input
            type="hidden"
            name="model_alias"
            value={defaultValues.model_alias}
          />
        </>
      ) : (
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-medium text-foreground">
            {t("form.modelAlias.label")}
          </span>
          <Select
            name="model_alias"
            value={selectedModel}
            onValueChange={setSelectedModel}
            placeholder={t("form.modelAlias.placeholder")}
            options={models.map((m) => ({ value: m.alias, label: m.alias }))}
            className="h-9"
          />
        </label>
      )}

      {/* Strategy */}
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-medium text-foreground">
          {t("form.strategy.label")}
        </span>
        <Select
          name="strategy"
          value={strategy}
          onValueChange={setStrategy}
          placeholder={t("form.strategy.placeholder")}
          options={strategies.map((s) => ({
            value: s,
            label: t(`strategy.${s}`),
          }))}
          className="h-9"
        />
      </label>

      {/* Providers */}
      <div className="flex flex-col gap-3">
        <span className="text-sm font-medium text-foreground">
          {t("columns.providers")}
        </span>
        {rows.map((row, index) => (
          <RouteProviderRow
            key={row.key}
            providers={allowedProviders}
            defaultValue={defaultValues?.providers?.[index]}
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
          {t("form.providers.add")}
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

      <div className={modalFormActionsClass}>
        <Button type="button" variant="outline" onClick={onCancel}>
          {tCommon("actions.cancel")}
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? t("actions.saving") : isEdit ? tCommon("actions.edit") : t("actions.create")}
        </Button>
      </div>
    </form>
  );
}
