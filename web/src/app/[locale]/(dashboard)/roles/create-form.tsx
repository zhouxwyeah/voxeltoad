"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createRole, updateRole } from "./actions";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";
import type { RoleRow, PermissionItem } from "./page";

type RoleFormProps = {
  permissions: PermissionItem[];
  defaultValues?: RoleRow | null;
  onSuccess?: () => void;
  onCancel?: () => void;
};

/** Group permissions by resource (the part before the dot). */
function groupPermissions(perms: PermissionItem[]): Map<string, PermissionItem[]> {
  const groups = new Map<string, PermissionItem[]>();
  for (const p of perms) {
    const resource = p.perm.split(".")[0];
    const list = groups.get(resource) ?? [];
    list.push(p);
    groups.set(resource, list);
  }
  return groups;
}

export function RoleForm({
  permissions,
  defaultValues,
  onSuccess,
  onCancel,
}: RoleFormProps) {
  const isEdit = !!defaultValues;
  const t = useTranslations("roles");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(
    isEdit ? updateRole : createRole,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  const [scopeKind, setScopeKind] = useState(
    defaultValues?.scope_kind ?? "tenant",
  );
  const [description, setDescription] = useState(
    defaultValues?.description ?? "",
  );

  // Initialize checked permissions from defaultValues or empty.
  const [checked, setChecked] = useState<Set<string>>(() => {
    if (defaultValues?.permissions) {
      return new Set(defaultValues.permissions);
    }
    return new Set<string>();
  });

  // Also track wildcard state separately for a special toggle.
  const [wildcard, setWildcard] = useState(
    defaultValues?.permissions?.includes("*") ?? false,
  );

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      onSuccessRef.current?.();
      router.refresh();
    }
  }, [state?.ok, router]);

  const toggle = (perm: string) => {
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(perm)) {
        next.delete(perm);
      } else {
        next.add(perm);
      }
      return next;
    });
  };

  const toggleWildcard = () => {
    const newVal = !wildcard;
    setWildcard(newVal);
    if (newVal) {
      setChecked(new Set(["*"]));
    } else {
      setChecked(new Set());
    }
  };

  // Build the permissions CSV value for the hidden form field.
  const permsValue = wildcard ? "*" : Array.from(checked).join(",");

  const grouped = groupPermissions(permissions);
  const resources = Array.from(grouped.keys()).sort();

  return (
    <form ref={formRef} action={formAction}>
      {isEdit && <input type="hidden" name="role_id" value={defaultValues!.id} />}

      {/* Name */}
      <div className="mb-4">
        <label className="mb-1 block text-sm font-medium text-foreground">
          {t("form.name")}
        </label>
        <Input
          name="name"
          placeholder={t("form.namePlaceholder")}
          defaultValue={defaultValues?.name ?? ""}
          disabled={defaultValues?.is_builtin}
          required={!isEdit}
        />
        {defaultValues?.is_builtin && (
          <p className="mt-1 text-xs text-muted-foreground">{t("form.builtinNameLocked")}</p>
        )}
      </div>

      {/* Scope kind */}
      <div className="mb-4">
        <label className="mb-1 block text-sm font-medium text-foreground">
          {t("form.scope")}
        </label>
        {defaultValues?.is_builtin ? (
          <>
            <input type="hidden" name="scope_kind" value={scopeKind} />
            <p className="rounded border bg-muted px-3 py-2 text-sm text-muted-foreground">
              {scopeKind === "global" ? t("scope.global") : t("scope.tenant")}
              <span className="ml-2 text-xs">({t("form.builtinScopeLocked")})</span>
            </p>
          </>
        ) : (
          <Select
            name="scope_kind"
            options={[
              { value: "global", label: t("scope.global") },
              { value: "tenant", label: t("scope.tenant") },
            ]}
            value={scopeKind}
            onValueChange={(v) => setScopeKind(v as "global" | "tenant")}
          />
        )}
      </div>

      {/* Description */}
      <div className="mb-4">
        <label className="mb-1 block text-sm font-medium text-foreground">
          {t("form.description")}
        </label>
        <Input
          name="description"
          placeholder={t("form.descriptionPlaceholder")}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
      </div>

      {/* Permissions matrix */}
      <div className="mb-4">
        <label className="mb-2 block text-sm font-medium text-foreground">
          {t("form.permissions")}
        </label>

        {/* Wildcard toggle */}
        <label className="mb-3 flex cursor-pointer items-center gap-2">
          <input
            type="checkbox"
            checked={wildcard}
            onChange={toggleWildcard}
            className="h-4 w-4 rounded border-border accent-primary"
          />
          <span className="text-sm font-medium text-foreground">
            {t("form.wildcard")}
          </span>
          <span className="text-xs text-muted-foreground">
            ({t("form.wildcardHint")})
          </span>
        </label>

        {/* Permission grid */}
        {!wildcard && (
          <div className="max-h-64 overflow-y-auto rounded-md border">
            {resources.map((resource) => {
              const items = grouped.get(resource) ?? [];
              const reads = items.filter((p) => p.perm.endsWith(".read"));
              const writes = items.filter((p) => p.perm.endsWith(".write"));
              const others = items.filter(
                (p) => !p.perm.endsWith(".read") && !p.perm.endsWith(".write"),
              );

              return (
                <div
                  key={resource}
                  className="border-b last:border-0 px-3 py-2"
                >
                  <div className="text-xs font-semibold uppercase text-muted-foreground">
                    {resource}
                  </div>
                  <div className="mt-1 flex flex-wrap gap-2">
                    {[...reads, ...writes, ...others].map((p) => (
                      <label
                        key={p.perm}
                        className="flex cursor-pointer items-center gap-1.5 whitespace-nowrap rounded px-2 py-1 text-xs hover:bg-muted"
                      >
                        <input
                          type="checkbox"
                          checked={checked.has(p.perm)}
                          onChange={() => toggle(p.perm)}
                          className="h-3.5 w-3.5 rounded border-border accent-primary"
                        />
                        <span>{p.label}</span>
                      </label>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Hidden permissions field for form submission */}
      <input type="hidden" name="permissions" value={permsValue} />

      {/* Error */}
      {state && !state.ok && (
        <div className="mb-4 rounded bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {(() => {
            const key = state.errorKey;
            if (key) {
              try {
                return tErr(`${key}` as Parameters<typeof tErr>[0]);
              } catch {
                return state.error;
              }
            }
            return state.error;
          })()}
        </div>
      )}

      {/* Actions */}
      <div className="flex gap-3">
        <Button variant="primary" type="submit" disabled={pending}>
          {pending
            ? tCommon("actions.saving")
            : isEdit
              ? tCommon("actions.save")
              : tCommon("actions.create")}
        </Button>
        <Button variant="outline" type="button" onClick={onCancel} disabled={pending}>
          {tCommon("actions.cancel")}
        </Button>
      </div>
    </form>
  );
}
