"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createProvider, updateProvider, testProviderConnection } from "./actions";
import type { ProviderTestOutcome, ProviderTestSpec } from "./actions";
import { Button, Input } from "@/components/ui";
import { modalFormActionsClass } from "@/components/modal";
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

/** One row of the endpoints dynamic array. */
type EndpointRow = { id: string; adapter: string; base_url: string };

/**
 * Provider form. Used inside a Modal for both create and edit (POST upsert
 * with defaultValue pre-fill).
 *
 * Endpoints is a dynamic array of (adapter, base_url) pairs (ADR-0049). type is
 * a brand select with a "Custom…" option. Submission uses parallel hidden
 * inputs named endpoint_adapter / endpoint_base_url / endpoint_id so the server
 * action reads them via FormData.getAll (same DOM-order zip pattern as the
 * upstream-row form).
 */
export function ProviderForm({
  defaultValues,
  onSuccess,
  onCancel,
}: {
  defaultValues?: Record<string, unknown> | null;
  onSuccess?: () => void;
  onCancel?: () => void;
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

  // ----- endpoints: dynamic array -----
  const dvEndpoints: EndpointRow[] = defaultValues?.endpoints
    ? (defaultValues.endpoints as EndpointRow[]).map((ep) => ({
        id: ep.id ?? "",
        adapter: ep.adapter ?? "",
        base_url: ep.base_url ?? "",
      }))
    : [];
  const initialEndpoints = dvEndpoints.length > 0 ? dvEndpoints : [{ id: "", adapter: "openai", base_url: "" }];
  const [endpoints, setEndpoints] = useState<EndpointRow[]>(initialEndpoints);

  function addEndpoint() {
    setEndpoints((eps) => [...eps, { id: "", adapter: "openai", base_url: "" }]);
  }
  function removeEndpoint(index: number) {
    setEndpoints((eps) => eps.filter((_, i) => i !== index));
  }

  // ----- credential: ref vs plaintext, mutually exclusive (single Select). -----
  const dvApiKeyRef = defaultValues?.api_key_ref ? String(defaultValues.api_key_ref) : "";
  const [credMode, setCredMode] = useState<CredMode>(
    dvApiKeyRef.startsWith(DB_PROVIDER_REF_PREFIX) ? "key" : "ref",
  );

  // ----- connectivity test (unsaved form values; nothing is persisted) -----
  const [testing, setTesting] = useState(false);
  const [testOutcome, setTestOutcome] = useState<ProviderTestOutcome | null>(null);

  async function runFormTest() {
    if (!formRef.current) return;
    const fd = new FormData(formRef.current);
    // Test against the first endpoint (the "primary"). A per-endpoint test is
    // a follow-up; the primary is the operator-facing "does this vendor respond".
    const adapters = fd.getAll("endpoint_adapter");
    const baseUrls = fd.getAll("endpoint_base_url");
    const adapterValue = String(adapters[0] ?? "").trim();
    const baseUrl = String(baseUrls[0] ?? "").trim();
    if (!adapterValue || !baseUrl) {
      setTestOutcome({ ok: false, error: t("test.missingFields") });
      return;
    }
    const spec: ProviderTestSpec = { adapter: adapterValue, baseUrl };
    if (isEdit) {
      spec.name = String(fd.get("name") ?? "").trim() || undefined;
    }
    if (credMode === "key") {
      const key = String(fd.get("api_key") ?? "").trim();
      if (key) spec.apiKey = key;
    } else {
      const ref = String(fd.get("api_key_ref") ?? "").trim();
      if (ref && ref !== "***" && !ref.endsWith("://***")) {
        spec.apiKeyRef = ref;
      }
    }
    setTesting(true);
    setTestOutcome(null);
    setTestOutcome(await testProviderConnection(spec));
    setTesting(false);
  }

  useEffect(() => {
    if (state?.ok && !success) {
      formRef.current?.reset();
      setSelectedType("");
      setCustomType("");
      setShowCustom(false);
      setEndpoints([{ id: "", adapter: "openai", base_url: "" }]);
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

      {/* Endpoints: dynamic array (ADR-0049) */}
      <div className="flex flex-col gap-2">
        <span className="font-medium text-foreground text-sm">
          {t("form.endpoints.label")}
        </span>
        <p className="text-xs text-muted-foreground -mt-1">
          {t("form.endpoints.help")}
        </p>
        {endpoints.map((ep, i) => (
          <div key={i} className="flex items-start gap-2 rounded-md border border-border p-2">
            {/* endpoint_id: optional slug input */}
            <input
              type="hidden"
              name="endpoint_id"
              value={ep.id}
            />
            <label className="flex flex-col gap-1 text-xs">
              <span className="text-muted-foreground">{t("form.endpoints.adapter")}</span>
              <Select
                name="endpoint_adapter"
                value={ep.adapter}
                onValueChange={(v) => {
                  setEndpoints((eps) => eps.map((e, j) => j === i ? { ...e, adapter: v } : e));
                }}
                options={[
                  { value: "openai", label: "openai" },
                  { value: "claude", label: "claude" },
                ]}
                className="h-8 w-28"
              />
            </label>
            <label className="flex flex-1 flex-col gap-1 text-xs">
              <span className="text-muted-foreground">{t("form.endpoints.baseUrl")}</span>
              <input
                name="endpoint_base_url"
                type="url"
                required
                value={ep.base_url}
                onChange={(e) => {
                  setEndpoints((eps) => eps.map((ee, j) => j === i ? { ...ee, base_url: e.target.value } : ee));
                }}
                placeholder={t("form.endpoints.baseUrlPlaceholder")}
                className="h-8 w-full rounded-md border border-border bg-background px-2 text-xs text-foreground"
              />
            </label>
            {endpoints.length > 1 && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="mt-5 h-8 px-2 text-destructive"
                onClick={() => removeEndpoint(i)}
              >
                ✕
              </Button>
            )}
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addEndpoint}
          className="w-fit"
        >
          + {t("form.endpoints.add")}
        </Button>
      </div>

      {/* Credential: a single Select chooses between two mutually exclusive
          inputs, so the two never submit together. */}
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

      {testOutcome && (
        <p
          role={testOutcome.ok ? "status" : "alert"}
          className={
            testOutcome.ok
              ? "w-full rounded-md bg-success/10 px-3 py-2 text-sm text-success"
              : "w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
          }
        >
          {testOutcome.ok
            ? t("test.success", { latency: testOutcome.latencyMs })
            : t("test.failed", {
                error: testOutcome.errorKey
                  ? tErr(testOutcome.errorKey)
                  : testOutcome.error,
              })}
        </p>
      )}

      <div className={modalFormActionsClass}>
        <Button
          type="button"
          variant="outline"
          onClick={runFormTest}
          disabled={testing || pending}
        >
          {testing ? t("test.testing") : t("test.action")}
        </Button>
        <Button type="button" variant="outline" onClick={onCancel ?? onSuccess}>
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
