"use client";

import { useTranslations } from "next-intl";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";

type RouteProvider = {
  name: string;
  weight?: number;
};

type ProviderOption = { name: string };

export function RouteProviderRow({
  providers,
  defaultValue,
  providerValue,
  onProviderChange,
  onRemove,
}: {
  providers: ProviderOption[];
  defaultValue?: RouteProvider;
  providerValue: string;
  onProviderChange: (v: string) => void;
  onRemove: () => void;
}) {
  const t = useTranslations("routes");

  return (
    <div className="grid grid-cols-[1fr_120px_auto] items-end gap-3 rounded-md border border-border p-3">
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-medium text-foreground">
          {t("form.providers.name.label")}
        </span>
        <Select
          name="route_provider_name"
          value={providerValue}
          onValueChange={onProviderChange}
          placeholder={t("form.providers.name.placeholder")}
          options={providers.map((p) => ({ value: p.name, label: p.name }))}
          className="h-9"
        />
      </label>
      <Input
        name="route_provider_weight"
        type="number"
        min={0}
        label={t("form.providers.weight.label")}
        placeholder={t("form.providers.weight.placeholder")}
        defaultValue={
          defaultValue?.weight !== undefined
            ? String(defaultValue.weight)
            : ""
        }
      />
      <Button type="button" variant="outline" size="sm" onClick={onRemove}>
        {t("form.providers.remove")}
      </Button>
    </div>
  );
}
