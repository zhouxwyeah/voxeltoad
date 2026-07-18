import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "../components/ui/button";
import { Field } from "../components/ui/field";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
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
import { createModel, deleteModel, listModels, listProviders, updateModel } from "../lib/api";
import { displayToMicro, microToDisplay } from "../lib/format";
import type { Model, ModelUpstream, Provider } from "../lib/types";

// Mirrors the admin models page (web/.../(dashboard)/models): table with
// inline upstream pills, create/edit Modal xl with the full metadata form
// (description/context_length/capabilities/tags + per-upstream max tokens,
// prices and cache-hit %). Prices display as major units and submit as
// int64 micro-units; currency is hardcoded "USD" like the admin form.
const CURRENCY = "USD";

type UpstreamDraft = {
  key: string;
  provider: string;
  upstream_model: string;
  max_tokens: string; // display: empty = no default
  prompt_price: string; // display major units, e.g. "2.50"
  completion_price: string;
  cache_hit_pct: string; // display 0-100; empty = unconfigured (full price)
};

function newUpstream(provider = ""): UpstreamDraft {
  return {
    key: crypto.randomUUID(),
    provider,
    upstream_model: "",
    max_tokens: "",
    prompt_price: "",
    completion_price: "",
    cache_hit_pct: "",
  };
}

function toDraft(u: ModelUpstream): UpstreamDraft {
  return {
    key: crypto.randomUUID(),
    provider: u.provider,
    upstream_model: u.upstream_model,
    max_tokens: u.default_max_tokens ? String(u.default_max_tokens) : "",
    prompt_price: u.pricing?.prompt_per_1m != null ? microToDisplay(u.pricing.prompt_per_1m) : "",
    completion_price:
      u.pricing?.completion_per_1m != null ? microToDisplay(u.pricing.completion_per_1m) : "",
    cache_hit_pct:
      u.pricing?.cache_hit_multiplier != null && u.pricing.cache_hit_multiplier > 0
        ? String(u.pricing.cache_hit_multiplier / 10_000)
        : "",
  };
}

export function Models() {
  const [rows, setRows] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editRow, setEditRow] = useState<Model | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const navigate = useNavigate();

  const load = () => {
    setLoading(true);
    Promise.all([listModels(), listProviders()])
      .then(([m, p]) => {
        setRows(m);
        setProviders(p);
        setError(null);
      })
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const hasProviders = providers.length > 0;

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
          <h1 className="text-xl font-semibold text-foreground">模型</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            模型别名到上游供应商的映射，含每百万 token 定价。
          </p>
        </div>
        {hasProviders && (
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            创建模型
          </Button>
        )}
      </div>

      {!hasProviders && (
        <div className="flex items-center justify-between rounded-md border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
          <span>创建模型前需要先添加至少一个供应商。</span>
          <Button variant="outline" size="sm" onClick={() => navigate("/providers")}>
            前往供应商页面
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
            <TableHead>别名</TableHead>
            <TableHead>上游</TableHead>
            <TableHead className="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={3} className="px-4 py-10 text-center text-muted-foreground">
                暂无模型
              </TableCell>
            </TableRow>
          ) : (
            rows.map((m) => (
              <TableRow key={m.alias}>
                <TableCell className="align-top">{m.alias}</TableCell>
                <TableCell className="align-top">
                  {(m.upstreams ?? []).length === 0 ? (
                    <span className="text-muted-foreground">无上游</span>
                  ) : (
                    <div className="flex flex-col gap-1">
                      {m.upstreams.map((u, i) => (
                        <span
                          key={`${u.provider}-${u.upstream_model}-${i}`}
                          className="inline-flex w-fit items-center rounded-full bg-muted px-2 py-0.5 text-xs text-foreground"
                        >
                          {u.provider} · {u.upstream_model}
                          {u.pricing &&
                            ` · ${microToDisplay(u.pricing.prompt_per_1m ?? 0)}/${microToDisplay(u.pricing.completion_per_1m ?? 0)} per 1M`}
                          {u.pricing?.cache_hit_multiplier
                            ? ` · cache ${(u.pricing.cache_hit_multiplier / 10_000).toString()}%`
                            : ""}
                        </span>
                      ))}
                    </div>
                  )}
                </TableCell>
                <TableCell className="align-top text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button variant="outline" size="sm" onClick={() => setEditRow(m)}>
                      编辑
                    </Button>
                    <Button variant="destructive" size="sm" onClick={() => setDeleting(m.alias)}>
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
      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="创建模型" size="xl">
        <ModelForm
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
      <Modal open={!!editRow} onClose={() => setEditRow(null)} title="编辑模型" size="xl">
        {editRow && (
          <ModelForm
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

      {/* Delete confirm — route references (409) render inline */}
      <ConfirmModal
        open={deleting !== null}
        onCancel={() => setDeleting(null)}
        onConfirm={async () => {
          if (deleting === null) return;
          await deleteModel(deleting);
          load();
        }}
        title="确认删除"
        message={deleting !== null ? `确定要删除模型「${deleting}」吗？需先移除引用它的路由。` : ""}
      />
    </div>
  );
}

/** Model create/edit form — mirrors the admin ModelForm field set. */
function ModelForm({
  providers,
  defaultValues,
  onSuccess,
  onCancel,
}: {
  providers: Provider[];
  defaultValues: Model | null;
  onSuccess: () => void;
  onCancel: () => void;
}) {
  const isEdit = !!defaultValues;
  const [alias, setAlias] = useState(defaultValues?.alias ?? "");
  const [description, setDescription] = useState(defaultValues?.description ?? "");
  const [contextLength, setContextLength] = useState(
    defaultValues?.context_length ? String(defaultValues.context_length) : "",
  );
  const [capabilities, setCapabilities] = useState((defaultValues?.capabilities ?? []).join(", "));
  const [tags, setTags] = useState((defaultValues?.tags ?? []).join(", "));
  const [upstreams, setUpstreams] = useState<UpstreamDraft[]>(() =>
    (defaultValues?.upstreams ?? []).length > 0
      ? (defaultValues?.upstreams ?? []).map(toDraft)
      : [newUpstream(providers[0]?.name)],
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const patchUpstream = (key: string, patch: Partial<UpstreamDraft>) =>
    setUpstreams((arr) => arr.map((u) => (u.key === key ? { ...u, ...patch } : u)));

  /** display "12.5" → micro; empty → fallback. Throws on malformed input. */
  const parsePrice = (v: string, fallback = 0) => (v.trim() === "" ? fallback : displayToMicro(v));

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!alias.trim()) {
      setError("请填写模型别名。");
      return;
    }
    if (upstreams.length === 0) {
      setError("至少需要一个上游。");
      return;
    }
    let parsed: ModelUpstream[];
    try {
      parsed = upstreams.map((u) => {
        if (!u.provider || !u.upstream_model.trim()) {
          throw new Error("每个上游都需要选择供应商并填写上游模型。");
        }
        const pct = u.cache_hit_pct.trim() === "" ? 0 : Number(u.cache_hit_pct);
        if (Number.isNaN(pct) || pct < 0 || pct > 100) {
          throw new Error("缓存命中 % 必须是 0-100 的数字。");
        }
        return {
          provider: u.provider,
          upstream_model: u.upstream_model.trim(),
          ...(u.max_tokens.trim() !== "" ? { default_max_tokens: Number(u.max_tokens) } : {}),
          pricing: {
            prompt_per_1m: parsePrice(u.prompt_price),
            completion_per_1m: parsePrice(u.completion_price),
            currency: CURRENCY,
            cache_hit_multiplier: Math.round(pct * 10_000),
          },
        };
      });
    } catch (err) {
      setError(err instanceof RangeError ? "价格必须是合法数字（如 2.50）。" : String((err as Error)?.message ?? err));
      return;
    }

    const body: Model = {
      alias: alias.trim(),
      ...(description.trim() !== "" ? { description: description.trim() } : {}),
      ...(contextLength.trim() !== "" ? { context_length: Number(contextLength) } : {}),
      ...(capabilities.trim() !== ""
        ? { capabilities: capabilities.split(",").map((s) => s.trim()).filter(Boolean) }
        : {}),
      ...(tags.trim() !== "" ? { tags: tags.split(",").map((s) => s.trim()).filter(Boolean) } : {}),
      upstreams: parsed,
    };

    setPending(true);
    try {
      const res = isEdit ? await updateModel(body.alias, body) : await createModel(body);
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
      <Field label="别名" required>
        <Input
          value={alias}
          onChange={(e) => setAlias(e.target.value)}
          placeholder="gpt-4o"
          disabled={isEdit}
          required
        />
      </Field>

      <Field label="描述">
        <Textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="模型的简介、适用场景等"
          rows={3}
        />
      </Field>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="上下文长度">
          <Input
            type="number"
            min={0}
            value={contextLength}
            onChange={(e) => setContextLength(e.target.value)}
            placeholder="128000"
          />
        </Field>
        <Field label="能力（逗号分隔）">
          <Input
            value={capabilities}
            onChange={(e) => setCapabilities(e.target.value)}
            placeholder="vision, function_calling, streaming"
          />
        </Field>
      </div>

      <Field label="标签（逗号分隔）">
        <Input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="chat, reasoning" />
      </Field>

      <div className="flex flex-col gap-3">
        <span className="text-sm font-medium text-foreground">上游</span>
        {upstreams.map((u) => (
          <div key={u.key} className="flex flex-col gap-3 rounded-md border border-border p-3">
            <div className="grid grid-cols-2 gap-3">
              <Field label="供应商" required>
                <Select
                  value={u.provider}
                  onChange={(e) => patchUpstream(u.key, { provider: e.target.value })}
                  required
                >
                  <option value="" disabled>
                    选择供应商
                  </option>
                  {providers.map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="上游模型" required>
                <Input
                  value={u.upstream_model}
                  onChange={(e) => patchUpstream(u.key, { upstream_model: e.target.value })}
                  placeholder="gpt-4o"
                  required
                />
              </Field>
            </div>
            <Field label="默认最大 token 数" hint="留空表示无默认值">
              <Input
                type="number"
                min={0}
                value={u.max_tokens}
                onChange={(e) => patchUpstream(u.key, { max_tokens: e.target.value })}
              />
            </Field>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              <Field label="输入价格 / 百万 token">
                <Input
                  value={u.prompt_price}
                  onChange={(e) => patchUpstream(u.key, { prompt_price: e.target.value })}
                  placeholder="2.50"
                />
              </Field>
              <Field label="输出价格 / 百万 token">
                <Input
                  value={u.completion_price}
                  onChange={(e) => patchUpstream(u.key, { completion_price: e.target.value })}
                  placeholder="10.00"
                />
              </Field>
              <Field label="缓存命中 %" hint="50 = 缓存 token 半价">
                <Input
                  type="number"
                  min={0}
                  max={100}
                  step={1}
                  value={u.cache_hit_pct}
                  onChange={(e) => patchUpstream(u.key, { cache_hit_pct: e.target.value })}
                />
              </Field>
            </div>
            <div className="flex justify-end">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setUpstreams((arr) => arr.filter((x) => x.key !== u.key))}
              >
                移除
              </Button>
            </div>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setUpstreams((arr) => [...arr, newUpstream(providers[0]?.name)])}
          >
            添加上游
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
          {pending ? "保存中…" : isEdit ? "保存" : "创建"}
        </Button>
      </div>
    </form>
  );
}
