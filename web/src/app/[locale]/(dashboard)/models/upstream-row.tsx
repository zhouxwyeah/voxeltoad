"use client";

import { useTranslations } from "next-intl";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";
import { microToDisplay } from "@/lib/money";

type ProviderOption = { name: string };

type UpstreamDefault = {
  provider?: string;
  upstream_model?: string;
  default_max_tokens?: number;
  pricing?: {
    prompt_per_1m?: number;
    completion_per_1m?: number;
    currency?: string;
    cache_hit_multiplier?: number;
  };
};

/**
 * One upstream row inside the model create/edit form. All fields use the SAME
 * `name` across every rendered row (not `upstreams[i].field` indexing) — see
 * create-form.tsx / actions.ts for why: FormData.getAll() zips same-name
 * fields by DOM order, so removing a row just shrinks the parallel arrays,
 * no index renumbering needed.
 *
 * The provider <Select> is controlled (value/onProviderChange) because the
 * shared <Select> primitive is controlled-only; its hidden input keeps the
 * name="upstream_provider" contract so the getAll() zip order is preserved.
 */
export function UpstreamRow({
  providers,
  defaultValues,
  providerValue,
  onProviderChange,
  onRemove,
}: {
  providers: ProviderOption[];
  defaultValues?: UpstreamDefault;
  providerValue: string;
  onProviderChange: (v: string) => void;
  onRemove: () => void;
}) {
  const t = useTranslations("models");
  const upstreamVal = defaultValues?.upstream_model ?? "";
  const maxTokensVal = defaultValues?.default_max_tokens
    ? String(defaultValues.default_max_tokens)
    : "";
  const promptPriceVal =
    defaultValues?.pricing?.prompt_per_1m != null
      ? microToDisplay(defaultValues.pricing.prompt_per_1m)
      : "";
  const completionPriceVal =
    defaultValues?.pricing?.completion_per_1m != null
      ? microToDisplay(defaultValues.pricing.completion_per_1m)
      : "";
  // cache_hit_multiplier: 1_000_000 micro = 100%. Display as integer percent.
  // Empty/0 = unconfigured = full price (legacy-safe).
  const cacheHitMulVal =
    defaultValues?.pricing?.cache_hit_multiplier != null &&
    defaultValues.pricing.cache_hit_multiplier > 0
      ? String(defaultValues.pricing.cache_hit_multiplier / 10_000)
      : "";

  return (
    <div className="flex flex-col gap-3 rounded-md border border-border p-3">
      <div className="grid grid-cols-2 gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-medium text-foreground">
            {t("form.upstreams.provider.label")}
          </span>
          <Select
            name="upstream_provider"
            value={providerValue}
            onValueChange={onProviderChange}
            placeholder={t("form.upstreams.provider.placeholder")}
            options={providers.map((p) => ({ value: p.name, label: p.name }))}
            className="h-9"
          />
        </label>
        <Input
          name="upstream_model"
          label={t("form.upstreams.upstreamModel.label")}
          placeholder={t("form.upstreams.upstreamModel.placeholder")}
          defaultValue={upstreamVal}
          required
        />
      </div>
      <Input
        name="upstream_max_tokens"
        type="number"
        min={0}
        label={t("form.upstreams.maxTokens.label")}
        placeholder={t("form.upstreams.maxTokens.placeholder")}
        defaultValue={maxTokensVal}
      />
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <Input
          name="upstream_prompt_price"
          label={t("form.upstreams.promptPrice.label")}
          placeholder={t("form.upstreams.promptPrice.placeholder")}
          defaultValue={promptPriceVal}
        />
        <Input
          name="upstream_completion_price"
          label={t("form.upstreams.completionPrice.label")}
          placeholder={t("form.upstreams.completionPrice.placeholder")}
          defaultValue={completionPriceVal}
        />
        <Input
          name="upstream_cache_hit_multiplier"
          type="number"
          min={0}
          max={100}
          step={1}
          label={t("form.upstreams.cacheHitMultiplier.label")}
          placeholder={t("form.upstreams.cacheHitMultiplier.placeholder")}
          defaultValue={cacheHitMulVal}
        />
      </div>
      <div className="flex justify-end">
        <Button type="button" variant="outline" size="sm" onClick={onRemove}>
          {t("form.upstreams.remove")}
        </Button>
      </div>
    </div>
  );
}
