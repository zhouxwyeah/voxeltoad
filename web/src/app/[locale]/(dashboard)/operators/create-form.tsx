"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { createOperator, updateOperator } from "./actions";
import { Button, Input } from "@/components/ui";
import { Select } from "@/components/ui/select";

type TenantOption = { id: number; name: string };
type RoleOption = { id: number; name: string; scope_kind: string };

type OperatorRow = {
  id: number;
  email: string;
  role: string;
  tenant_id?: number | null;
};

/**
 * Operator create/edit form. Used inside a Modal for both create and edit.
 * In edit mode: email is editable, password is optional (leave empty = keep
 * current), role is hidden (not editable per ADR-0017 design).
 *
 * Phase-2 RBAC: for create mode, shows ALL roles (built-in + custom) via
 * `/api/v1/roles`, passing `role_id` to the backend.
 */
export function OperatorForm({
  tenants,
  roles,
  defaultValues,
  onSuccess,
  onCancel,
}: {
  tenants: TenantOption[];
  roles: RoleOption[];
  defaultValues?: OperatorRow | null;
  onSuccess?: () => void;
  onCancel?: () => void;
}) {
  const isEdit = !!defaultValues;
  const t = useTranslations("operators");
  const tCommon = useTranslations("common");
  const tErr = useTranslations("errors");
  const [state, formAction, pending] = useActionState(
    isEdit ? updateOperator : createOperator,
    null,
  );
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const onSuccessRef = useRef(onSuccess);
  // eslint-disable-next-line react-hooks/refs
  onSuccessRef.current = onSuccess;

  // roleId: for create, the selected role's database id.
  const [roleId, setRoleId] = useState<string>("");
  // roleName: derived for display and to control tenant_id visibility.
  const selectedRole = roles.find((r) => String(r.id) === roleId);
  const [tenantId, setTenantId] = useState(
    defaultValues?.tenant_id != null ? String(defaultValues.tenant_id) : "",
  );

  // Build role options for the Select. Built-in roles come first.
  const builtinRoles = roles.filter((r) => r.name === "super-admin" || r.name === "tenant-admin");
  const customRoles = roles.filter((r) => r.name !== "super-admin" && r.name !== "tenant-admin");
  const roleOptions = [
    ...builtinRoles.map((r) => ({
      value: String(r.id),
      label: r.name === "super-admin"
        ? t("roles.super-admin")
        : r.name === "tenant-admin"
          ? t("roles.tenant-admin")
          : r.name,
    })),
    ...customRoles.map((r) => ({
      value: String(r.id),
      label: r.name,
    })),
  ];

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      onSuccessRef.current?.();
      router.refresh();
    }
  }, [state, router]);

  return (
    <form ref={formRef} action={formAction} className="flex flex-col gap-4">
      {/* Hidden id for edit mode */}
      {isEdit && !!defaultValues && (
        <input type="hidden" name="id" value={defaultValues.id} />
      )}

      <Input
        name="email"
        type="email"
        label={t("form.email.label")}
        placeholder={t("form.email.placeholder")}
        required={!isEdit}
        defaultValue={defaultValues?.email ?? ""}
      />

      {/* Password: required for create, optional for edit */}
      <Input
        name="password"
        type="password"
        label={isEdit ? t("form.password.editLabel") : t("form.password.label")}
        placeholder={
          isEdit
            ? t("form.password.editPlaceholder")
            : t("form.password.placeholder")
        }
        required={!isEdit}
        minLength={6}
      />

      {/* Role: selectable for create (via role_id), hidden for edit */}
      {isEdit ? (
        <input type="hidden" name="role_id" value={String(roleId)} />
      ) : (
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-medium text-foreground">
            {t("form.role.label")}
          </span>
          <input type="hidden" name="role_id" value={roleId} />
          <Select
            name="_role_select"
            value={roleId}
            onValueChange={setRoleId}
            placeholder={t("form.role.placeholder")}
            options={roleOptions}
          />
        </label>
      )}

      {/* Tenant select: show if role scope_kind is "tenant" */}
      {selectedRole?.scope_kind === "tenant" && (
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-medium text-foreground">
            {t("form.tenantId.label")}
          </span>
          <Select
            name="tenant_id"
            value={tenantId}
            onValueChange={setTenantId}
            placeholder={t("form.tenantId.placeholder")}
            options={tenants.map((tenant) => ({
              value: String(tenant.id),
              label: tenant.name,
            }))}
          />
        </label>
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
          {pending ? tCommon("actions.saving") : isEdit ? t("actions.save") : t("actions.save")}
        </Button>
      </div>
    </form>
  );
}
