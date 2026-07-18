import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import { Field } from "../components/ui/field";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { Modal } from "../components/ui/modal";
import { listProviders, createProvider, updateProvider, deleteProvider } from "../lib/api";
import type { Provider } from "../lib/types";

// Common provider brands shown in the type Select. The backend treats
// `type` as a free-form brand label (internal/config/schema.go); selecting
// CUSTOM_TYPE falls back to an Input so users can enter anything.
const PROVIDER_TYPES = ["openai", "tencent", "zhipu", "anthropic", "compatible"] as const;
const CUSTOM_TYPE = "__custom__";

const EMPTY: Provider = {
  name: "",
  type: "openai",
  adapter: "openai",
  base_url: "",
  api_key_ref: "plain://",
  timeouts: { connect: 2_000_000_000, first_byte: 5_000_000_000, overall: 30_000_000_000 },
  weight: 100,
};

// Backend stores timeouts as Go durations (nanoseconds); the form edits in seconds.
const NS_PER_S = 1_000_000_000;
const nsToS = (ns: number) => ns / NS_PER_S;
const sToNs = (s: number) => Math.round(s * NS_PER_S);

export function Providers() {
  const [rows, setRows] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Provider | null>(null); // non-null = edit/create modal open
  const [viewing, setViewing] = useState<Provider | null>(null); // non-null = detail modal open
  const [isNew, setIsNew] = useState(false);

  const load = () => {
    setLoading(true);
    listProviders()
      .then(setRows)
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const onSave = async () => {
    if (!editing) return;
    try {
      if (isNew) await createProvider(editing);
      else await updateProvider(editing.name, editing);
      setEditing(null);
      load();
    } catch (e) {
      setError(String((e as Error)?.message ?? e));
    }
  };
  const onDelete = async (name: string) => {
    if (!confirm(`删除供应商 ${name}?`)) return;
    try {
      await deleteProvider(name);
      load();
    } catch (e) {
      setError(String((e as Error)?.message ?? e));
    }
  };

  if (loading) {
    return <div className="p-6"><Skeleton className="h-8 w-40" /><div className="mt-4 space-y-2">{Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-12" />)}</div></div>;
  }

  return (
    <div className="mx-auto max-w-6xl p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">供应商</h1>
        <Button onClick={() => { setEditing({ ...EMPTY }); setIsNew(true); }}>+ 新增</Button>
      </div>
      {error && <div className="mt-3 rounded border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</div>}

      {rows.length === 0 && !editing && <EmptyState title="暂无供应商" description="点击「新增」添加第一个供应商" />}

      <div className="mt-4 overflow-hidden rounded-lg border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs uppercase text-muted-foreground">
            <tr><th className="p-3">名称</th><th className="p-3">类型</th><th className="p-3">adapter</th><th className="p-3">base_url</th><th className="p-3">权重</th><th className="p-3"></th></tr>
          </thead>
          <tbody>
            {rows.map((p) => (
              <tr key={p.name} className="border-t">
                <td className="p-3 font-medium">{p.name}</td>
                <td className="p-3"><Badge>{p.type}</Badge></td>
                <td className="p-3 font-mono text-xs">{p.adapter}</td>
                <td className="p-3 font-mono text-xs text-muted-foreground truncate max-w-xs">{p.base_url}</td>
                <td className="p-3">{p.weight}</td>
                <td className="p-3 text-right whitespace-nowrap">
                  <Button variant="ghost" size="sm" onClick={() => setViewing({ ...p })}>详情</Button>
                  <Button variant="ghost" size="sm" onClick={() => { setEditing({ ...p }); setIsNew(false); }}>编辑</Button>
                  <Button variant="ghost" size="sm" onClick={() => onDelete(p.name)}>删除</Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Detail modal (read-only) */}
      <Modal open={!!viewing} onClose={() => setViewing(null)} title={viewing ? `供应商 ${viewing.name}` : ""} size="lg">
        {viewing && (
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
            <DetailField label="名称" value={viewing.name} />
            <DetailField label="类型" value={viewing.type} />
            <DetailField label="adapter" value={viewing.adapter} mono />
            <DetailField label="权重" value={String(viewing.weight)} />
            <DetailField label="base_url" value={viewing.base_url} mono full />
            <DetailField label="api_key_ref" value={viewing.api_key_ref} mono full />
            <DetailField label="timeouts.connect" value={`${viewing.timeouts.connect / 1_000_000_000}s`} mono />
            <DetailField label="timeouts.first_byte" value={`${viewing.timeouts.first_byte / 1_000_000_000}s`} mono />
            <DetailField label="timeouts.overall" value={`${viewing.timeouts.overall / 1_000_000_000}s`} mono />
          </dl>
        )}
      </Modal>

      {/* Edit / create modal */}
      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={isNew ? "新增供应商" : editing ? `编辑 ${editing.name}` : ""}
        size="xl"
        footer={
          <>
            <Button onClick={onSave}>保存</Button>
            <Button variant="ghost" onClick={() => setEditing(null)}>取消</Button>
          </>
        }
      >
        {editing && (
          <div className="space-y-5">
            <FormSection title="基本信息">
              <Field label="名称" required>
                <Input value={editing.name} disabled={!isNew} onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
              </Field>
              <TypeField
                value={editing.type}
                onChange={(t) => setEditing({ ...editing, type: t })}
              />
              <Field label="Adapter" required>
                <Select value={editing.adapter} onChange={(e) => setEditing({ ...editing, adapter: e.target.value })}>
                  <option value="openai">openai</option>
                  <option value="claude">claude</option>
                </Select>
              </Field>
              <Field label="权重" required>
                <Input type="number" value={editing.weight} onChange={(e) => setEditing({ ...editing, weight: Number(e.target.value) })} />
              </Field>
            </FormSection>

            <FormSection title="连接与超时">
              <Field label="Base URL" required full>
                <Input value={editing.base_url} onChange={(e) => setEditing({ ...editing, base_url: e.target.value })} />
              </Field>
              <div className="sm:col-span-2 grid gap-3 sm:grid-cols-3">
                <Field label="连接超时" suffix="s">
                  <Input
                    type="number"
                    step="0.1"
                    min="0"
                    value={nsToS(editing.timeouts.connect)}
                    onChange={(e) =>
                      setEditing({
                        ...editing,
                        timeouts: { ...editing.timeouts, connect: sToNs(Number(e.target.value)) },
                      })
                    }
                  />
                </Field>
                <Field label="首字节超时" suffix="s">
                  <Input
                    type="number"
                    step="0.1"
                    min="0"
                    value={nsToS(editing.timeouts.first_byte)}
                    onChange={(e) =>
                      setEditing({
                        ...editing,
                        timeouts: { ...editing.timeouts, first_byte: sToNs(Number(e.target.value)) },
                      })
                    }
                  />
                </Field>
                <Field label="整体超时" suffix="s">
                  <Input
                    type="number"
                    step="0.1"
                    min="0"
                    value={nsToS(editing.timeouts.overall)}
                    onChange={(e) =>
                      setEditing({
                        ...editing,
                        timeouts: { ...editing.timeouts, overall: sToNs(Number(e.target.value)) },
                      })
                    }
                  />
                </Field>
              </div>
            </FormSection>

            <FormSection title="密钥">
              <Field
                label="API Key Ref"
                required
                full
                hint="env://VAR · db://provider/<name> · plain://literal(仅开发)"
              >
                <Input value={editing.api_key_ref} onChange={(e) => setEditing({ ...editing, api_key_ref: e.target.value })} />
              </Field>
            </FormSection>
          </div>
        )}
      </Modal>
    </div>
  );
}

function DetailField({ label, value, mono, full }: { label: string; value: string; mono?: boolean; full?: boolean }) {
  return (
    <div className={full ? "sm:col-span-2" : ""}>
      <dt className="text-xs uppercase text-muted-foreground">{label}</dt>
      <dd className={`mt-0.5 ${mono ? "font-mono text-xs" : ""}`}>{value || "—"}</dd>
    </div>
  );
}

function FormSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="mb-2 text-sm font-medium text-muted-foreground">{title}</h3>
      <div className="grid gap-3 sm:grid-cols-2">{children}</div>
    </section>
  );
}

function TypeField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const isPreset = (PROVIDER_TYPES as readonly string[]).includes(value);
  return (
    <>
      <Field label="类型" required>
        {isPreset ? (
          <Select
            value={value}
            onChange={(e) => {
              const v = e.target.value;
              onChange(v === CUSTOM_TYPE ? "" : v);
            }}
          >
            {PROVIDER_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
            <option value={CUSTOM_TYPE}>自定义…</option>
          </Select>
        ) : (
          <div className="flex gap-2">
            <Input
              className="flex-1"
              value={value}
              onChange={(e) => onChange(e.target.value)}
              placeholder="例如 openai-compatible"
            />
            <Select
              className="w-32 shrink-0"
              value={CUSTOM_TYPE}
              onChange={(e) => {
                const v = e.target.value;
                if (v !== CUSTOM_TYPE) onChange(v);
              }}
            >
              <option value={CUSTOM_TYPE}>自定义</option>
              {PROVIDER_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </Select>
          </div>
        )}
      </Field>
    </>
  );
}
