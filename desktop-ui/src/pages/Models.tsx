import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { Card, CardContent } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Field } from "../components/ui/field";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { Modal } from "../components/ui/modal";
import { listModels, createModel, updateModel, deleteModel, listProviders } from "../lib/api";
import type { Model, Provider, ModelUpstream } from "../lib/types";

const CURRENCIES = ["usd", "cny"] as const;

function emptyUpstream(provider?: string): ModelUpstream {
  return { provider: provider ?? "", upstream_model: "", pricing: { prompt_per_1m: 0, completion_per_1m: 0, currency: "usd" } };
}
const EMPTY: Model = { alias: "", upstreams: [emptyUpstream()] };

export function Models() {
  const [rows, setRows] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Model | null>(null);
  const [viewing, setViewing] = useState<Model | null>(null);
  const [isNew, setIsNew] = useState(false);

  const load = () => {
    setLoading(true);
    Promise.all([listModels(), listProviders()])
      .then(([m, p]) => { setRows(m); setProviders(p); })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const providerNames = providers.map((p) => p.name);

  const onSave = async () => {
    if (!editing) return;
    try {
      if (isNew) await createModel(editing);
      else await updateModel(editing.alias, editing);
      setEditing(null);
      load();
    } catch (e) { setError(String((e as Error)?.message ?? e)); }
  };
  const onDelete = async (alias: string) => {
    if (!confirm(`删除模型 ${alias}?`)) return;
    try { await deleteModel(alias); load(); }
    catch (e) { setError(String((e as Error)?.message ?? e)); }
  };

  const setU = (i: number, patch: Partial<ModelUpstream>) => {
    if (!editing) return;
    const ups = editing.upstreams.map((u, idx) => (idx === i ? { ...u, ...patch } : u));
    setEditing({ ...editing, upstreams: ups });
  };
  const addU = () => editing && setEditing({ ...editing, upstreams: [...editing.upstreams, emptyUpstream(providerNames[0])] });
  const delU = (i: number) => editing && setEditing({ ...editing, upstreams: editing.upstreams.filter((_, idx) => idx !== i) });

  if (loading) return <div className="p-6"><Skeleton className="h-8 w-40" /><div className="mt-4 space-y-2">{Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-12" />)}</div></div>;

  return (
    <div className="mx-auto max-w-6xl p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">模型</h1>
        <Button onClick={() => { setEditing({ ...EMPTY, upstreams: [emptyUpstream(providerNames[0])] }); setIsNew(true); }}>+ 新增</Button>
      </div>
      {error && <div className="mt-3 rounded border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</div>}

      {rows.length === 0 && !editing && <EmptyState title="暂无模型" description="点击「新增」添加第一个模型别名" />}

      <div className="mt-4 space-y-3">
        {rows.map((m) => (
          <Card key={m.alias}>
            <CardContent className="flex items-start justify-between gap-3 pt-4">
              <div>
                <div className="flex items-center gap-2">
                  <span className="font-semibold">{m.alias}</span>
                  {m.description && <span className="text-xs text-muted-foreground">{m.description}</span>}
                </div>
                <div className="mt-2 space-y-1">
                  {m.upstreams.map((u, i) => (
                    <div key={i} className="text-xs text-muted-foreground">
                      <Badge tone="muted">{u.provider}</Badge> <span className="font-mono">{u.upstream_model}</span>
                      <span className="ml-2">${u.pricing.prompt_per_1m / 1_000_000}/${u.pricing.completion_per_1m / 1_000_000} per 1M ({u.pricing.currency})</span>
                    </div>
                  ))}
                </div>
              </div>
              <div className="flex gap-1">
                <Button variant="ghost" size="sm" onClick={() => setViewing(JSON.parse(JSON.stringify(m)))}>详情</Button>
                <Button variant="ghost" size="sm" onClick={() => { setEditing(JSON.parse(JSON.stringify(m))); setIsNew(false); }}>编辑</Button>
                <Button variant="ghost" size="sm" onClick={() => onDelete(m.alias)}>删除</Button>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Detail modal */}
      <Modal open={!!viewing} onClose={() => setViewing(null)} title={viewing ? `模型 ${viewing.alias}` : ""} size="lg">
        {viewing && (
          <div className="space-y-3">
            {viewing.description && <p className="text-sm text-muted-foreground">{viewing.description}</p>}
            <div className="rounded-lg border">
              <table className="w-full text-sm">
                <thead className="bg-muted/50 text-left text-xs uppercase text-muted-foreground">
                  <tr><th className="p-2">provider</th><th className="p-2">upstream_model</th><th className="p-2">prompt/1M</th><th className="p-2">completion/1M</th><th className="p-2">币种</th></tr>
                </thead>
                <tbody>
                  {viewing.upstreams.map((u, i) => (
                    <tr key={i} className="border-t">
                      <td className="p-2"><Badge tone="muted">{u.provider}</Badge></td>
                      <td className="p-2 font-mono text-xs">{u.upstream_model}</td>
                      <td className="p-2 font-mono text-xs">{(u.pricing.prompt_per_1m / 1_000_000).toFixed(2)}</td>
                      <td className="p-2 font-mono text-xs">{(u.pricing.completion_per_1m / 1_000_000).toFixed(2)}</td>
                      <td className="p-2 text-xs">{u.pricing.currency}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </Modal>

      {/* Edit / create modal */}
      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={isNew ? "新增模型" : editing ? `编辑 ${editing.alias}` : ""}
        size="2xl"
        footer={
          <>
            <Button onClick={onSave}>保存</Button>
            <Button variant="ghost" onClick={() => setEditing(null)}>取消</Button>
          </>
        }
      >
        {editing && (
          <div className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="别名" required>
                <Input value={editing.alias} disabled={!isNew} onChange={(e) => setEditing({ ...editing, alias: e.target.value })} />
              </Field>
              <Field label="描述(可选)">
                <Input value={editing.description ?? ""} onChange={(e) => setEditing({ ...editing, description: e.target.value })} />
              </Field>
            </div>

            <section>
              <h3 className="mb-2 text-sm font-medium text-muted-foreground">上游 (upstreams)</h3>
              <div className="space-y-2">
                {editing.upstreams.map((u, i) => (
                  <div key={i} className="relative rounded-lg border border-border p-3">
                    {/* Delete button pinned to the card's top-right corner so it
                        visually belongs to the whole upstream, not one row. */}
                    <Button
                      variant="ghost"
                      size="sm"
                      className="absolute right-2 top-2 h-7 w-7 p-0"
                      onClick={() => delU(i)}
                      aria-label="删除上游"
                    >
                      <X className="h-4 w-4" />
                    </Button>
                    {/* Row 1: provider + upstream_model. Provider list is short
                        brand names; upstream_model is the long identifier — give
                        it the wider column. */}
                    <div className="grid gap-2 sm:grid-cols-[2fr_3fr]">
                      <Field label="Provider" required>
                        <Select value={u.provider} onChange={(e) => setU(i, { provider: e.target.value })}>
                          {providerNames.map((n) => <option key={n} value={n}>{n}</option>)}
                        </Select>
                      </Field>
                      <Field label="上游模型" required>
                        <Input value={u.upstream_model} onChange={(e) => setU(i, { upstream_model: e.target.value })} />
                      </Field>
                    </div>
                    {/* Row 2: pricing triple. Unit compressed into the label
                        so both rows fit on one line; the "/ 1M" scope is
                        identical for prompt/completion and stated once. */}
                    <div className="mt-2 grid gap-2 sm:grid-cols-3">
                      <Field label="Prompt / 1M (micro)" required>
                        <Input
                          type="number"
                          min="0"
                          value={u.pricing.prompt_per_1m}
                          onChange={(e) => setU(i, { pricing: { ...u.pricing, prompt_per_1m: Number(e.target.value) } })}
                        />
                      </Field>
                      <Field label="Completion / 1M (micro)" required>
                        <Input
                          type="number"
                          min="0"
                          value={u.pricing.completion_per_1m}
                          onChange={(e) => setU(i, { pricing: { ...u.pricing, completion_per_1m: Number(e.target.value) } })}
                        />
                      </Field>
                      <Field label="币种" required>
                        <Select
                          value={u.pricing.currency}
                          onChange={(e) => setU(i, { pricing: { ...u.pricing, currency: e.target.value } })}
                        >
                          {CURRENCIES.map((c) => (
                            <option key={c} value={c}>{c}</option>
                          ))}
                        </Select>
                      </Field>
                    </div>
                  </div>
                ))}
              </div>
              <Button variant="ghost" size="sm" className="mt-2" onClick={addU}>+ 添加上游</Button>
            </section>
          </div>
        )}
      </Modal>
    </div>
  );
}
