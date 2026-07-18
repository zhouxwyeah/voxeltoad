import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "../components/ui/button";
import { Field } from "../components/ui/field";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Skeleton } from "../components/ui/skeleton";
import { Modal, modalFormActionsClass } from "../components/ui/modal";
import { ConfirmModal } from "../components/ui/confirm-modal";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";
import { createProvider, deleteProvider, listProviders, updateProvider } from "../lib/api";
import type { Provider } from "../lib/types";

// Mirrors the admin providers page (web/.../(dashboard)/providers) — same
// columns, same form fields, same modal sizes. Fields the admin form does not
// have (weight / timeouts) are hidden here but still required by the desktop
// data plane (zero timeouts = unprotected upstream calls), so creates send
// defaults and edits preserve the stored values.
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
const CUSTOM_TYPE = "_custom_";
const DEFAULT_TIMEOUTS = { connect: 2_000_000_000, first_byte: 5_000_000_000, overall: 30_000_000_000 };
const DEFAULT_WEIGHT = 100;

/** Prefix of a literal (plaintext) credential stored in the local YAML. */
const PLAIN_REF_PREFIX = "plain://";
type CredMode = "ref" | "key";

export function Providers() {
  const [rows, setRows] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<Provider | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    listProviders()
      .then((r) => {
        setRows(r);
        setError(null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  if (loading) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-4 w-64" />
        <div className="space-y-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-11" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      {/* Page header (admin template: title + subtitle + primary action) */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">供应商</h1>
          <p className="mt-1 text-sm text-muted-foreground">网关代理的上游 LLM 服务。</p>
        </div>
        <Button variant="primary" onClick={() => setCreateOpen(true)}>
          创建供应商
        </Button>
      </div>

      {error && (
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>名称</TableHead>
            <TableHead>类型</TableHead>
            <TableHead>适配器</TableHead>
            <TableHead>基础 URL</TableHead>
            <TableHead className="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={5} className="px-4 py-10 text-center text-muted-foreground">
                暂无供应商。
              </TableCell>
            </TableRow>
          ) : (
            rows.map((p) => (
              <TableRow key={p.name}>
                <TableCell>{p.name}</TableCell>
                <TableCell>{p.type}</TableCell>
                <TableCell>{p.adapter}</TableCell>
                <TableCell>{p.base_url}</TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setEditRow(p)}>
                      编辑
                    </Button>
                    <Button variant="destructive" size="sm" onClick={() => setDeleting(p.name)}>
                      删除
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>

      {/* Create modal */}
      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="创建供应商" size="lg">
        <ProviderForm
          defaultValues={null}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => {
            setCreateOpen(false);
            load();
          }}
        />
      </Modal>

      {/* Edit modal */}
      <Modal open={!!editRow} onClose={() => setEditRow(null)} title="编辑供应商" size="lg">
        {editRow && (
          <ProviderForm
            defaultValues={editRow}
            onCancel={() => setEditRow(null)}
            onSuccess={() => {
              setEditRow(null);
              load();
            }}
          />
        )}
      </Modal>

      {/* Delete confirm — reference conflicts (409) render inline */}
      <ConfirmModal
        open={deleting !== null}
        onCancel={() => setDeleting(null)}
        onConfirm={async () => {
          if (deleting === null) return;
          await deleteProvider(deleting);
          load();
        }}
        title="确认删除"
        message={deleting !== null ? `删除供应商 "${deleting}"?` : ""}
      />
    </div>
  );
}

/**
 * Provider create/edit form — field-for-field mirror of the admin
 * ProviderForm: name, brand type (preset + custom), adapter, base_url, and a
 * credential-mode select (reference vs plaintext key). On desktop a plaintext
 * key is persisted as a `plain://` ref in the local YAML (there is no
 * encrypted credential store); weight/timeouts are carried through unchanged.
 */
function ProviderForm({
  defaultValues,
  onSuccess,
  onCancel,
}: {
  defaultValues: Provider | null;
  onSuccess: () => void;
  onCancel: () => void;
}) {
  const isEdit = !!defaultValues;

  const [name, setName] = useState(defaultValues?.name ?? "");
  const dvType = defaultValues?.type ?? "";
  const dvIsPreset = PRESET_BRANDS.includes(dvType);
  const [typeSelect, setTypeSelect] = useState(dvIsPreset ? dvType : dvType ? CUSTOM_TYPE : "");
  const [customType, setCustomType] = useState(!dvIsPreset && dvType ? dvType : "");
  const [adapter, setAdapter] = useState(defaultValues?.adapter ?? "");
  const [baseURL, setBaseURL] = useState(defaultValues?.base_url ?? "");
  const dvApiKeyRef = defaultValues?.api_key_ref ?? "";
  const [credMode, setCredMode] = useState<CredMode>(
    dvApiKeyRef.startsWith(PLAIN_REF_PREFIX) ? "key" : "ref",
  );
  const [apiKeyRef, setApiKeyRef] = useState(dvApiKeyRef.startsWith(PLAIN_REF_PREFIX) ? "" : dvApiKeyRef);
  const [apiKey, setApiKey] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const typeValue = typeSelect === CUSTOM_TYPE ? customType.trim() : typeSelect;

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const ref =
      credMode === "ref"
        ? apiKeyRef.trim()
        : apiKey
          ? `${PLAIN_REF_PREFIX}${apiKey}`
          : dvApiKeyRef; // key mode + blank on edit = leave unchanged
    if (!name.trim() || !typeValue || !adapter || !baseURL.trim() || !ref) {
      setError("请完整填写名称、类型、适配器、基础 URL 与凭证。");
      return;
    }
    setPending(true);
    try {
      // Hidden fields: creates get data-plane-safe defaults; edits preserve
      // the stored weight/timeouts (the form never edits them).
      const body: Provider = {
        timeouts: defaultValues?.timeouts ?? { ...DEFAULT_TIMEOUTS },
        weight: defaultValues?.weight ?? DEFAULT_WEIGHT,
        name: name.trim(),
        type: typeValue,
        adapter,
        base_url: baseURL.trim(),
        api_key_ref: ref,
      };
      const res = isEdit ? await updateProvider(body.name, body) : await createProvider(body);
      toast.success(isEdit ? "供应商已更新。" : "供应商已创建。");
      if (res.warning) toast.warning(res.warning);
      onSuccess();
    } catch (err) {
      setError(String((err as Error)?.message ?? err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-4">
      <Field label="名称" required>
        <Input value={name} onChange={(e) => setName(e.target.value)} disabled={isEdit} required />
      </Field>

      {/* Type: brand dropdown + custom text */}
      <Field label="类型" required>
        <Select value={typeSelect} onChange={(e) => setTypeSelect(e.target.value)} required>
          <option value="" disabled>
            选择品牌
          </option>
          {PRESET_BRANDS.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
          <option value={CUSTOM_TYPE}>自定义…</option>
        </Select>
      </Field>
      {typeSelect === CUSTOM_TYPE && (
        <Input
          value={customType}
          onChange={(e) => setCustomType(e.target.value)}
          placeholder="输入品牌名称"
          required
        />
      )}

      <Field label="适配器" required>
        <Select value={adapter} onChange={(e) => setAdapter(e.target.value)} required>
          <option value="" disabled>
            选择适配器
          </option>
          <option value="openai">openai</option>
          <option value="claude">claude</option>
        </Select>
      </Field>

      <Field label="基础 URL" required>
        <Input
          type="url"
          value={baseURL}
          onChange={(e) => setBaseURL(e.target.value)}
          placeholder="https://…"
          required
        />
      </Field>

      {/* Credential: one Select chooses between two mutually exclusive inputs. */}
      <Field label="凭证方式" required>
        <Select value={credMode} onChange={(e) => setCredMode(e.target.value as CredMode)}>
          <option value="ref">引用模式（env://…）</option>
          <option value="key">直接输入（本地存储）</option>
        </Select>
      </Field>
      {credMode === "ref" ? (
        <Field label="API 密钥引用" required>
          <Input
            value={apiKeyRef}
            onChange={(e) => setApiKeyRef(e.target.value)}
            placeholder="env://KEY"
            required
          />
        </Field>
      ) : (
        <Field
          label="API 密钥"
          required={!isEdit}
          hint="明文密钥将以 plain:// 形式保存在本地配置文件中；编辑时留空表示保持不变。"
        >
          <Input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="sk-…"
            autoComplete="new-password"
          />
        </Field>
      )}

      {error && (
        <p role="alert" className="w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      <div className={modalFormActionsClass}>
        <Button type="button" variant="outline" onClick={onCancel}>
          取消
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? "保存中…" : isEdit ? "保存" : "创建"}
        </Button>
      </div>
    </form>
  );
}
