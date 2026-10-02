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
import { createProvider, deleteProvider, getProviderHealth, listProviders, updateProvider } from "../lib/api";
import type { EndpointHealth, Provider } from "../lib/types";
import { formatDuration } from "../lib/format";

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
const DEFAULT_TIMEOUTS = { connect: 5_000_000_000, first_byte: 120_000_000_000, overall: 300_000_000_000 };
const DEFAULT_WEIGHT = 100;

/** Prefix of a literal (plaintext) credential stored in the local YAML. */
const PLAIN_REF_PREFIX = "plain://";
type CredMode = "ref" | "key";

type EndpointRow = {
  key: string;
  id: string;
  adapter: string;
  base_url: string;
};

function newEndpointRow(): EndpointRow {
  return { key: crypto.randomUUID(), id: "", adapter: "openai", base_url: "" };
}

function endpointToRow(ep: { id?: string; adapter: string; base_url: string }): EndpointRow {
  return { key: crypto.randomUUID(), id: ep.id ?? "", adapter: ep.adapter, base_url: ep.base_url };
}

export function Providers() {
  const [rows, setRows] = useState<Provider[]>([]);
  const [healthMap, setHealthMap] = useState<Map<string, EndpointHealth[]>>(new Map());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<Provider | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    Promise.all([listProviders(), getProviderHealth().catch(() => [])])
      .then(([r, health]) => {
        setRows(r);
        const map = new Map<string, EndpointHealth[]>();
        for (const h of health) {
          const list = map.get(h.provider) ?? [];
          list.push(h);
          map.set(h.provider, list);
        }
        setHealthMap(map);
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
            <TableHead>端点</TableHead>
            <TableHead>状态</TableHead>
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
            rows.map((p) => {
              const epHealth = healthMap.get(p.name) ?? [];
              return (
              <TableRow key={p.name}>
                <TableCell>{p.name}</TableCell>
                <TableCell>{p.type}</TableCell>
                <TableCell>
                  {p.endpoints && p.endpoints.length > 0 ? (
                    <div className="flex flex-col gap-1">
                      {p.endpoints.map((ep, i) => {
                        const label = ep.adapter === "claude" ? "Anthropic" : ep.adapter === "openai" ? "OpenAI" : ep.adapter;
                        return (
                          <div key={i} className="flex items-center gap-2">
                            <span className="inline-flex w-fit items-center rounded-full bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground">
                              {label}
                            </span>
                            <span className="text-xs text-muted-foreground">{ep.base_url}</span>
                          </div>
                        );
                      })}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell>
                  <HealthBadges health={epHealth} />
                </TableCell>
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
              );
            })
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
  const [endpoints, setEndpoints] = useState<EndpointRow[]>(
    defaultValues?.endpoints && defaultValues.endpoints.length > 0
      ? defaultValues.endpoints.map(endpointToRow)
      : [newEndpointRow()],
  );
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
    if (!name.trim() || !typeValue || !ref) {
      setError("请完整填写名称、类型与凭证。");
      return;
    }
    if (endpoints.length === 0) {
      setError("至少需要一个端点。");
      return;
    }
    if (endpoints.some((ep) => !ep.adapter || !ep.base_url.trim())) {
      setError("每个端点都需要选择协议并填写基础 URL。");
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
        endpoints: endpoints.map((ep) => ({
          ...(ep.id ? { id: ep.id } : {}),
          adapter: ep.adapter,
          base_url: ep.base_url.trim(),
        })),
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

      <div className="flex flex-col gap-2">
        <span className="text-sm font-medium text-foreground">端点</span>
        <p className="text-xs text-muted-foreground">
          配置一个或多个 (协议, 基础 URL) 端点。运行时按入站协议自动选端点（ADR-0049）。
        </p>
        {endpoints.map((ep) => (
          <div key={ep.key} className="flex flex-col gap-3 rounded-md border border-border p-3">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label="协议" required>
                <Select
                  value={ep.adapter}
                  onChange={(e) =>
                    setEndpoints((arr) => arr.map((x) => (x.key === ep.key ? { ...x, adapter: e.target.value } : x)))
                  }
                  required
                >
                  <option value="openai">openai</option>
                  <option value="claude">claude</option>
                </Select>
              </Field>
              <Field label="基础 URL" required>
                <Input
                  type="url"
                  value={ep.base_url}
                  onChange={(e) =>
                    setEndpoints((arr) => arr.map((x) => (x.key === ep.key ? { ...x, base_url: e.target.value } : x)))
                  }
                  placeholder="https://…"
                  required
                />
              </Field>
            </div>
            {endpoints.length > 1 && (
              <div className="flex justify-end">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setEndpoints((arr) => arr.filter((x) => x.key !== ep.key))}
                >
                  移除
                </Button>
              </div>
            )}
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setEndpoints((arr) => [...arr, newEndpointRow()])}
          >
            添加端点
          </Button>
        </div>
      </div>

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

/** HealthBadges renders the passive health state for a provider's endpoints.
 * Shows breaker state (closed/open/half-open/unknown) + success rate + last
 * seen time. ADR-0057. */
function HealthBadges({ health }: { health: EndpointHealth[] }) {
  if (health.length === 0) {
    return <span className="text-xs text-muted-foreground">未使用</span>;
  }
  return (
    <div className="flex flex-col gap-1">
      {health.map((h, i) => {
        const breakerLabel =
          h.breaker_state === "closed" ? "健康"
          : h.breaker_state === "open" ? "熔断"
          : h.breaker_state === "half-open" ? "半开"
          : "未知";
        const breakerColor =
          h.breaker_state === "closed" ? "bg-success/10 text-success"
          : h.breaker_state === "open" ? "bg-destructive/10 text-destructive"
          : h.breaker_state === "half-open" ? "bg-warning/10 text-warning"
          : "bg-muted text-muted-foreground";
        const successRate = h.attempted > 0 ? Math.round((h.selected / h.attempted) * 100) : null;
        const lastSeen = h.last_seen ? formatTimeShort(h.last_seen) : null;
        return (
          <div key={i} className="flex items-center gap-2 text-xs">
            <span className={`inline-flex w-fit items-center rounded-full px-2 py-0.5 font-medium ${breakerColor}`}>
              {breakerLabel}
            </span>
            {successRate !== null && (
              <span className="text-muted-foreground">{successRate}% 成功</span>
            )}
            {h.attempted > 0 && h.avg_duration_ms > 0 && (
              <span className="text-muted-foreground">{formatDuration(h.avg_duration_ms)}</span>
            )}
            {lastSeen && (
              <span className="text-muted-foreground" title={h.last_seen ?? ""}>{lastSeen}</span>
            )}
          </div>
        );
      })}
    </div>
  );
}

function formatTimeShort(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
