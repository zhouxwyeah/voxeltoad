import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "../components/ui/button";
import { Field } from "../components/ui/field";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { DetailField } from "../components/ui/detail-field";
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
import { createRoute, deleteRoute, listModels, listProviders, listRoutes, updateRoute } from "../lib/api";
import type { Model, Provider, Route, RouteProvider } from "../lib/types";

// Mirrors the admin routes page (web/.../(dashboard)/routes): strategy pill +
// provider pills in the table, detail Modal md with DetailFields, create/edit
// Modal xl where model_alias is a Select from existing models and candidate
// providers are filtered to the selected model's upstream providers.
const STRATEGIES = ["priority", "weighted", "round_robin", "session_affinity"] as const;
type Strategy = (typeof STRATEGIES)[number];

const STRATEGY_LABELS: Record<Strategy, string> = {
  priority: "优先级",
  weighted: "加权",
  round_robin: "轮询",
  session_affinity: "会话亲和",
};

function strategyLabel(s: string | undefined): string {
  return (STRATEGY_LABELS as Record<string, string>)[s ?? ""] ?? s ?? "-";
}

type ProviderRowDraft = { key: string; name: string; weight: string };

export function Routes() {
  const [rows, setRows] = useState<Route[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<Route | null>(null);
  const [detailRow, setDetailRow] = useState<Route | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const navigate = useNavigate();

  const load = () => {
    setLoading(true);
    Promise.all([listRoutes(), listModels(), listProviders()])
      .then(([r, m, p]) => {
        setRows(r);
        setModels(m);
        setProviders(p);
        setError(null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const hasModels = models.length > 0;

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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">路由</h1>
          <p className="mt-1 text-sm text-muted-foreground">将模型别名解析为候选供应商及选择策略。</p>
        </div>
        {hasModels && (
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            创建路由
          </Button>
        )}
      </div>

      {!hasModels && (
        <div className="flex items-center justify-between rounded-md border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
          <span>请先创建模型再添加路由。</span>
          <Button variant="outline" size="sm" onClick={() => navigate("/models")}>
            前往模型
          </Button>
        </div>
      )}

      {error && (
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>模型别名</TableHead>
            <TableHead>策略</TableHead>
            <TableHead>供应商</TableHead>
            <TableHead className="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={4} className="px-4 py-10 text-center text-muted-foreground">
                暂无路由。
              </TableCell>
            </TableRow>
          ) : (
            rows.map((r) => (
              <TableRow key={r.model_alias}>
                <TableCell className="align-top">{r.model_alias}</TableCell>
                <TableCell className="align-top">
                  {r.strategy ? (
                    <span className="inline-flex rounded-full bg-muted px-2 py-0.5 text-xs text-foreground">
                      {strategyLabel(r.strategy)}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="align-top">
                  {(r.providers ?? []).length === 0 ? (
                    <span className="text-muted-foreground">暂无供应商</span>
                  ) : (
                    <div className="flex flex-col gap-1">
                      {r.providers.map((p, i) => (
                        <span
                          key={`${p.name}-${i}`}
                          className="inline-flex w-fit items-center rounded-full bg-muted px-2 py-0.5 text-xs text-foreground"
                        >
                          {p.name}
                          {p.weight !== undefined && ` ·${p.weight}`}
                        </span>
                      ))}
                    </div>
                  )}
                </TableCell>
                <TableCell className="align-top text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setDetailRow(r)}>
                      查看
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => setEditRow(r)}>
                      编辑
                    </Button>
                    <Button
                      variant="destructive"
                      size="sm"
                      onClick={() => setDeleting(r.model_alias)}
                    >
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
      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="创建路由" size="xl">
        <RouteForm
          models={models}
          providers={providers}
          defaultValues={null}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => {
            setCreateOpen(false);
            load();
          }}
        />
      </Modal>

      {/* Edit modal */}
      <Modal open={!!editRow} onClose={() => setEditRow(null)} title="编辑路由" size="xl">
        {editRow && (
          <RouteForm
            models={models}
            providers={providers}
            defaultValues={editRow}
            onCancel={() => setEditRow(null)}
            onSuccess={() => {
              setEditRow(null);
              load();
            }}
          />
        )}
      </Modal>

      {/* Detail modal */}
      <Modal open={!!detailRow} onClose={() => setDetailRow(null)} title="路由详情" size="md">
        {detailRow && (
          <div className="flex flex-col gap-4">
            <DetailField label="模型别名">{detailRow.model_alias}</DetailField>
            <DetailField label="策略">{strategyLabel(detailRow.strategy)}</DetailField>
            <DetailField label="供应商">
              {(detailRow.providers ?? []).length === 0 ? (
                <span className="text-muted-foreground">暂无供应商</span>
              ) : (
                <span className="flex flex-col gap-1">
                  {(detailRow.providers ?? []).map((p, i) => (
                    <span key={i}>
                      {p.name}
                      {p.weight !== undefined && ` · weight: ${p.weight}`}
                    </span>
                  ))}
                </span>
              )}
            </DetailField>
          </div>
        )}
      </Modal>

      {/* Delete confirm */}
      <ConfirmModal
        open={deleting !== null}
        onCancel={() => setDeleting(null)}
        onConfirm={async () => {
          if (deleting === null) return;
          await deleteRoute(deleting);
          load();
        }}
        title="确认删除"
        message={deleting !== null ? `确认删除路由 "${deleting}"？` : ""}
      />
    </div>
  );
}

/** Route create/edit form — mirrors the admin RouteForm field set. */
function RouteForm({
  models,
  providers,
  defaultValues,
  onSuccess,
  onCancel,
}: {
  models: Model[];
  providers: Provider[];
  defaultValues: Route | null;
  onSuccess: () => void;
  onCancel: () => void;
}) {
  const isEdit = !!defaultValues;
  const [selectedModel, setSelectedModel] = useState(defaultValues?.model_alias ?? "");
  const [strategy, setStrategy] = useState(defaultValues?.strategy ?? "");
  const [rows, setRows] = useState<ProviderRowDraft[]>(() =>
    (defaultValues?.providers ?? []).map((p) => ({
      key: crypto.randomUUID(),
      name: p.name,
      weight: p.weight !== undefined ? String(p.weight) : "",
    })),
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Candidate providers are limited to the selected model's upstream
  // providers (a route pointing anywhere else could never serve the model).
  const allowedProviders = useMemo(() => {
    if (!selectedModel) return providers;
    const model = models.find((m) => m.alias === selectedModel);
    if (!model?.upstreams) return providers;
    const upstreamNames = new Set(model.upstreams.map((u) => u.provider));
    return providers.filter((p) => upstreamNames.has(p.name));
  }, [selectedModel, models, providers]);

  const patchRow = (key: string, patch: Partial<ProviderRowDraft>) =>
    setRows((arr) => arr.map((r) => (r.key === key ? { ...r, ...patch } : r)));

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!selectedModel) {
      setError("请选择模型别名。");
      return;
    }
    if (!strategy) {
      setError("请选择策略。");
      return;
    }
    if (rows.length === 0 || rows.some((r) => !r.name)) {
      setError("至少需要一个候选供应商，且每行都要选择供应商。");
      return;
    }
    const parsedProviders: RouteProvider[] = rows.map((r) => ({
      name: r.name,
      ...(r.weight.trim() !== "" ? { weight: Number(r.weight) } : {}),
    }));
    const body: Route = { model_alias: selectedModel, strategy, providers: parsedProviders };

    setPending(true);
    try {
      const res = isEdit ? await updateRoute(body.model_alias, body) : await createRoute(body);
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
      {/* Model alias: Select on create, locked input on edit */}
      {isEdit ? (
        <Field label="模型别名" required>
          <Input value={defaultValues.model_alias} disabled />
        </Field>
      ) : (
        <Field label="模型别名" required>
          <Select value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)} required>
            <option value="" disabled>
              选择模型
            </option>
            {models.map((m) => (
              <option key={m.alias} value={m.alias}>
                {m.alias}
              </option>
            ))}
          </Select>
        </Field>
      )}

      <Field label="策略" required>
        <Select value={strategy} onChange={(e) => setStrategy(e.target.value)} required>
          <option value="" disabled>
            选择策略
          </option>
          {STRATEGIES.map((s) => (
            <option key={s} value={s}>
              {STRATEGY_LABELS[s]}
            </option>
          ))}
        </Select>
      </Field>

      <div className="flex flex-col gap-3">
        <span className="text-sm font-medium text-foreground">供应商</span>
        {rows.map((row) => (
          <div
            key={row.key}
            className="grid grid-cols-[1fr_120px_auto] items-end gap-3 rounded-md border border-border p-3"
          >
            <Field label="供应商" required>
              <Select
                value={row.name}
                onChange={(e) => patchRow(row.key, { name: e.target.value })}
                required
              >
                <option value="" disabled>
                  选择供应商
                </option>
                {allowedProviders.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="权重">
              <Input
                type="number"
                min={0}
                value={row.weight}
                onChange={(e) => patchRow(row.key, { weight: e.target.value })}
                placeholder="1"
              />
            </Field>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setRows((arr) => arr.filter((x) => x.key !== row.key))}
            >
              移除
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setRows((arr) => [...arr, { key: crypto.randomUUID(), name: "", weight: "" }])}
          >
            添加供应商
          </Button>
        </div>
      </div>

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
          {pending ? "保存中…" : isEdit ? "编辑" : "创建"}
        </Button>
      </div>
    </form>
  );
}
