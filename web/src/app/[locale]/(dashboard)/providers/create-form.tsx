"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createProvider, updateProvider } from "./actions";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";
import { toast } from "@/lib/toast";

const PRESET_BRANDS = [
  "openai",
  "tencent",
  "zhipu",
  "anthropic",
  "google",
  "azure",
  "deepseek",
  "bedrock",
];

/** Prefix of an ADR-0030 db-stored encrypted credential reference. */
const DB_PROVIDER_REF_PREFIX = "db://provider/";
/** Credential input modes — mutually exclusive by construction (single Select). */
type CredMode = "ref" | "key";

/**
 * Provider form. Used inside a Modal for both create and edit (POST upsert
 * with defaultValue pre-fill).
 *
 * adapter is a two-value select (openai|claude).  type is a brand select
 * with a "Custom…" option that reveals a free-text input; submission uses a
 * hidden <input name="type"> so the server action always reads formData.get("type").
 */
export function ProviderForm({
  defaultValues,
  onSuccess,
}: {
  defaultValues?: Record<string, unknown> | null;
  onSuccess?: () => void;
}) {
  const isEdit = !!defaultValues;
  const t = useTranslations("providers");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(
    isEdit ? updateProvider : createProvider,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  // ----- type: brand dropdown + custom-text hybrid -----
  const dvType = defaultValues?.type ? String(defaultValues.type) : "";
  const dvIsPreset = PRESET_BRANDS.includes(dvType);
  const [selectedType, setSelectedType] = useState(dvIsPreset ? dvType : "");
  const [customType, setCustomType] = useState(!dvIsPreset && dvType !== "" ? dvType : "");
  const [showCustom, setShowCustom] = useState(!dvIsPreset && dvType !== "");
  const typeValue = selectedType !== "" ? selectedType : customType;
  const [success, setSuccess] = useState(false);

  // ----- adapter: two-value select -----
  const dvAdapter = defaultValues?.adapter ? String(defaultValues.adapter) : "";
  const [adapter, setAdapter] = useState(dvAdapter);

  // ----- credential: ref vs plaintext, mutually exclusive (single Select).
  // On edit, if the stored ref points at the encrypted credential store
  // (db://provider/<name>), default to "key" mode since that provider's
  // credential is gateway-managed; otherwise default to "ref". -----
  const dvApiKeyRef = defaultValues?.api_key_ref ? String(defaultValues.api_key_ref) : "";
  const [credMode, setCredMode] = useState<CredMode>(
    dvApiKeyRef.startsWith(DB_PROVIDER_REF_PREFIX) ? "key" : "ref",
  );

  useEffect(() => {
    if (state?.ok && !success) {
      formRef.current?.reset();
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSelectedType("");
      setCustomType("");
      setShowCustom(false);
      setAdapter("");
      setCredMode("ref");
      setSuccess(true);
      toast.success(t(isEdit ? "form.successUpdated" : "form.successCreated"));
      onSuccessRef.current?.();
      router.refresh();
    }
  }, [state, router, success]);

  return (
    <form
      ref={formRef}
      action={formAction}
      className="flex flex-col gap-4"
    >
      <Input
        name="name"
        label={t("form.name.label")}
        required
        defaultValue={defaultValues?.name ? String(defaultValues.name) : ""}
        disabled={isEdit}
      />
      {isEdit && (
        <input
          type="hidden"
          name="name"
          value={String(defaultValues?.name ?? "")}
        />
      )}

      {/* Type: brand dropdown + custom text */}
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-medium text-foreground">
          {t("form.type.label")}
        </span>
        <Select
          name="_type_select"
          value={selectedType !== "" ? selectedType : showCustom ? "_custom_" : ""}
          onValueChange={(v) => {
            if (v === "_custom_") {
              setSelectedType("");
              setShowCustom(true);
            } else {
              setSelectedType(v);
              setCustomType("");
              setShowCustom(false);
            }
          }}
          placeholder={t("form.type.placeholder")}
          options={[
            ...PRESET_BRANDS.map((b) => ({ value: b, label: b })),
            { value: "_custom_", label: t("form.type.custom") },
          ]}
          className="h-9"
        />
      </label>
      {showCustom && (
        <Input
          value={customType}
          onChange={(e) => setCustomType(e.target.value)}
          placeholder={t("form.type.customPlaceholder")}
        />
      )}
      <input type="hidden" name="type" value={typeValue} />

      {/* Adapter: two-value select */}
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-medium text-foreground">
          {t("form.adapter.label")}
        </span>
        <Select
          name="adapter"
          value={adapter}
          onValueChange={setAdapter}
          placeholder={t("form.adapter.placeholder")}
          options={[
            { value: "openai", label: "openai" },
            { value: "claude", label: "claude" },
          ]}
          className="h-9"
        />
      </label>

      <Input
        name="base_url"
        type="url"
        required
        label={t("form.baseUrl.label")}
        placeholder={t("form.baseUrl.placeholder")}
        defaultValue={defaultValues?.base_url ? String(defaultValues.base_url) : ""}
      />
      {/* Credential: a single Select chooses between two mutually exclusive
          inputs, so the two never submit together. "Reference" sends
          api_key_ref (env://…); "Plaintext" sends api_key, which the gateway
          encrypts and stores, rewriting the ref to db://provider/<name>
          (ADR-0030). On edit, plaintext is write-only — blank means "leave
          unchanged". */}
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-medium text-foreground">
          {t("form.credMode.label")}
        </span>
        <Select
          name="_cred_mode"
          value={credMode}
          onValueChange={(v) => setCredMode(v as CredMode)}
          options={[
            { value: "ref", label: t("form.credMode.ref") },
            { value: "key", label: t("form.credMode.key") },
          ]}
          className="h-9"
        />
      </label>
      {credMode === "ref" ? (
        <Input
          name="api_key_ref"
          label={t("form.apiKeyRef.label")}
          placeholder={t("form.apiKeyRef.placeholder")}
          defaultValue={dvApiKeyRef}
        />
      ) : (
        <>
          <Input
            name="api_key"
            type="password"
            label={t("form.apiKey.label")}
            placeholder={t("form.apiKey.placeholder")}
            autoComplete="new-password"
          />
          <p className="-mt-2 text-xs text-muted-foreground">
            {t("form.apiKey.hint")}
          </p>
        </>
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
        <Button type="button" variant="outline" onClick={onSuccess}>
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
