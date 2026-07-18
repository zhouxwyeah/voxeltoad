import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { EmptyState } from "../components/ui/empty-state";
import { Modal } from "../components/ui/modal";
import { listRoutes, createRoute, updateRoute, deleteRoute, listProviders } from "../lib/api";
import type { Route, Provider, RouteProvider } from "../lib/types";

const STRATEGIES = ["priority", "round_robin", "session_affinity"];
const EMPTY: Route = { model_alias: "", strategy: "priority", providers: [{ name: "", weight: 1 }] };

export function Routes() {
  const [rows, setRows] = useState<Route[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Route | null>(null);
  const [viewing, setViewing] = useState<Route | null>(null);
  const [isNew, setIsNew] = useState(false);

  const load = () => {
    setLoading(true);
    Promise.all([listRoutes(), listProviders()])
      .then(([r, p]) => { setRows(r); setProviders(p); })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const providerNames = providers.map((p) => p.name);

  const onSave = async () => {
    if (!editing) return;
    try {
      if (isNew) await createRoute(editing);
      else await updateRoute(editing.model_alias, editing);
      setEditing(null);
      load();
    } catch (e) { setError(String((e as Error)?.message ?? e)); }
  };
  const onDelete = async (alias: string) => {
    if (!confirm(`删除路由 ${alias}?`)) return;
    try { await deleteRoute(alias); load(); }
    catch (e) { setError(String((e as Error)?.message ?? e)); }
  };

  const setRP = (i: number, patch: Partial<RouteProvider>) => {
    if (!editing) return;
    const ps = editing.providers.map((rp, idx) => (idx === i ? { ...rp, ...patch } : rp));
    setEditing({ ...editing, providers: ps });
  };
  const addRP = () => editing && setEditing({ ...editing, providers: [...editing.providers, { name: providerNames[0] ?? "", weight: 1 }] });
  const delRP = (i: number) => editing && setEditing({ ...editing, providers: editing.providers.filter((_, idx) => idx !== i) });

  if (loading) return <div className="p-6"><Skeleton className="h-8 w-40" /><div className="mt-4 space-y-2">{Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-12" />)}</div></div>;

  return (
    <div className="mx-auto max-w-6xl p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">路由</h1>
        <Button onClick={() => { setEditing({ ...EMPTY, providers: [{ name: providerNames[0] ?? "", weight: 1 }] }); setIsNew(true); }}>+ 新增</Button>
      </div>
      {error && <div className="mt-3 rounded border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</div>}

      {rows.length === 0 && !editing && <EmptyState title="暂无路由" description="点击「新增」添加第一个路由" />}

      <div className="mt-4 overflow-hidden rounded-lg border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs uppercase text-muted-foreground">
            <tr><th className="p-3">model_alias</th><th className="p-3">策略</th><th className="p-3">providers</th><th className="p-3"></th></tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.model_alias} className="border-t">
                <td className="p-3 font-medium">{r.model_alias}</td>
                <td className="p-3"><Badge>{r.strategy}</Badge></td>
                <td className="p-3 text-xs text-muted-foreground">
                  {r.providers.map((rp) => `${rp.name}(w=${rp.weight ?? 1})`).join(" → ")}
                </td>
                <td className="p-3 text-right whitespace-nowrap">
                  <Button variant="ghost" size="sm" onClick={() => setViewing(JSON.parse(JSON.stringify(r)))}>详情</Button>
                  <Button variant="ghost" size="sm" onClick={() => { setEditing(JSON.parse(JSON.stringify(r))); setIsNew(false); }}>编辑</Button>
                  <Button variant="ghost" size="sm" onClick={() => onDelete(r.model_alias)}>删除</Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Detail modal */}
      <Modal open={!!viewing} onClose={() => setViewing(null)} title={viewing ? `路由 ${viewing.model_alias}` : ""} size="md">
        {viewing && (
          <div className="space-y-3 text-sm">
            <div>
              <span className="text-xs uppercase text-muted-foreground">策略</span>
              <div className="mt-0.5"><Badge>{viewing.strategy}</Badge></div>
            </div>
            <div>
              <span className="text-xs uppercase text-muted-foreground">候选 providers</span>
              <div className="mt-1 space-y-1">
                {viewing.providers.map((rp, i) => (
                  <div key={i} className="flex items-center gap-2 rounded border px-3 py-1.5">
                    <Badge tone="muted">{rp.name}</Badge>
                    <span className="text-xs text-muted-foreground">权重 {rp.weight ?? 1}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </Modal>

      {/* Edit / create modal */}
      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={isNew ? "新增路由" : editing ? `编辑 ${editing.model_alias}` : ""}
        size="lg"
        footer={
          <>
            <Button onClick={onSave}>保存</Button>
            <Button variant="ghost" onClick={() => setEditing(null)}>取消</Button>
          </>
        }
      >
        {editing && (
          <div className="space-y-3">
            <label className="block text-sm">model_alias
              <Input value={editing.model_alias} disabled={!isNew} onChange={(e) => setEditing({ ...editing, model_alias: e.target.value })} />
            </label>
            <label className="block text-sm">策略
              <Select value={editing.strategy} onChange={(e) => setEditing({ ...editing, strategy: e.target.value })}>
                {STRATEGIES.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
            </label>
            <div className="rounded-lg border p-3">
              <div className="mb-2 text-sm font-medium">候选 providers(顺序对 priority/round_robin 有意义)</div>
              {editing.providers.map((rp, i) => (
                <div key={i} className="mb-2 grid gap-2 sm:grid-cols-[1fr_auto_auto]">
                  <Select value={rp.name} onChange={(e) => setRP(i, { name: e.target.value })}>
                    {providerNames.map((n) => <option key={n} value={n}>{n}</option>)}
                  </Select>
                  <Input type="number" placeholder="权重" value={rp.weight ?? 1} onChange={(e) => setRP(i, { weight: Number(e.target.value) })} className="w-24" />
                  <Button variant="ghost" size="sm" onClick={() => delRP(i)}>×</Button>
                </div>
              ))}
              <Button variant="ghost" size="sm" onClick={addRP}>+ 添加 provider</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
