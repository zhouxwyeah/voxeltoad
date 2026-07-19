"use client";

import { useState } from "react";
import { useRouter } from "@/i18n/navigation";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal, ConfirmModal } from "@/components/modal";
import { microToDisplay } from "@/lib/money";
import { ModelForm } from "../create-form";
import { deleteModel } from "../actions";

type Pricing = {
  prompt_per_1m?: number;
  completion_per_1m?: number;
  currency?: string;
};

type ModelUpstream = {
  provider: string;
  upstream_model: string;
  default_max_tokens?: number;
  pricing?: Pricing;
};

type CatalogModel = {
  alias: string;
  description?: string;
  context_length?: number;
  capabilities?: string[];
  tags?: string[];
  upstreams?: ModelUpstream[];
};

type ProviderOption = { name: string };

/**
 * Full model detail view: description, capabilities, tags, context length, and
 * the complete upstream provider table with per-upstream pricing.
 *
 * super-admin (canWrite=true) additionally sees Edit/Delete buttons that open
 * the shared ModelForm Modal (edit) or a ConfirmModal (delete). tenant-admin
 * sees a purely read-only view.
 */
export function ModelDetailClient({
  model,
  canWrite,
  providers,
}: {
  model: CatalogModel | null;
  canWrite: boolean;
  providers: ProviderOption[];
}) {
  const t = useTranslations("models");
  const tCommon = useTranslations("common");
  const router = useRouter();
  const [editOpen, setEditOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<CatalogModel | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  if (!model) {
    return (
      <>
        <Button href="/models" variant="outline" size="sm">
          ← {t("detail.back")}
        </Button>
        <p className="text-sm text-muted-foreground">{t("notFound")}</p>
      </>
    );
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setDeleteError(null);
    const res = await deleteModel(deleteTarget.alias);
    if (res.ok) {
      setDeleteTarget(null);
      router.push("/models");
    } else {
      setDeleteError(res.error);
    }
    setDeleteLoading(false);
  }

  const upstreams = model.upstreams ?? [];
  const capabilities = model.capabilities ?? [];
  const tags = model.tags ?? [];

  return (
    <>
      <Button href="/models" variant="outline" size="sm">
        ← {t("detail.back")}
      </Button>

      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="text-xl font-semibold text-foreground">{model.alias}</h1>
          {model.context_length ? (
            <p className="text-sm text-muted-foreground">
              {t("detail.contextLength")}:{" "}
              <span className="font-medium text-foreground">
                {model.context_length.toLocaleString()}
              </span>
            </p>
          ) : null}
        </div>
        {canWrite && (
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setEditOpen(true)}>
              {t("actions.edit")}
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => setDeleteTarget(model)}
            >
              {t("actions.delete")}
            </Button>
          </div>
        )}
      </div>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-foreground">
          {t("detail.description")}
        </h2>
        <p className="text-sm text-foreground whitespace-pre-wrap">
          {model.description || t("card.noDescription")}
        </p>
      </section>

      {capabilities.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-semibold text-foreground">
            {t("detail.capabilities")}
          </h2>
          <div className="flex flex-wrap gap-1.5">
            {capabilities.map((c) => (
              <span
                key={c}
                className="inline-flex items-center rounded-full bg-muted px-2.5 py-0.5 text-xs text-foreground"
              >
                {c}
              </span>
            ))}
          </div>
        </section>
      )}

      {tags.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-semibold text-foreground">
            {t("detail.tags")}
          </h2>
          <div className="flex flex-wrap gap-1.5">
            {tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center rounded border border-border px-2 py-0.5 text-xs text-muted-foreground"
              >
                {tag}
              </span>
            ))}
          </div>
        </section>
      )}

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-foreground">
          {t("detail.upstreams")}
        </h2>
        <div className="overflow-hidden rounded-lg border border-border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <th className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.provider")}
                </th>
                <th className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.upstreamModel")}
                </th>
                <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.promptPrice")}
                </th>
                <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.completionPrice")}
                </th>
                <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("detail.maxTokens")}
                </th>
              </tr>
            </thead>
            <tbody>
              {upstreams.length === 0 ? (
                <tr>
                  <td
                    colSpan={5}
                    className="px-4 py-6 text-center text-muted-foreground"
                  >
                    —
                  </td>
                </tr>
              ) : (
                upstreams.map((u, i) => (
                  <tr
                    key={`${u.provider}-${u.upstream_model}-${i}`}
                    className="border-b border-border last:border-b-0"
                  >
                    <td className="px-4 py-2 text-foreground">{u.provider}</td>
                    <td className="px-4 py-2 text-foreground">{u.upstream_model}</td>
                    <td className="px-4 py-2 text-right tabular-nums text-foreground">
                      {u.pricing?.prompt_per_1m !== undefined
                        ? microToDisplay(u.pricing.prompt_per_1m)
                        : "—"}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums text-foreground">
                      {u.pricing?.completion_per_1m !== undefined
                        ? microToDisplay(u.pricing.completion_per_1m)
                        : "—"}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums text-foreground">
                      {u.default_max_tokens
                        ? u.default_max_tokens.toLocaleString()
                        : "—"}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      {canWrite && (
        <Modal
          open={editOpen}
          onClose={() => setEditOpen(false)}
          title={t("modal.editTitle")}
          size="lg"
        >
          <ModelForm
            providers={providers}
            defaultValues={model}
            onCancel={() => setEditOpen(false)}
            onSuccess={() => setEditOpen(false)}
          />
        </Modal>
      )}

      <ConfirmModal
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={tCommon("modal.confirmDelete")}
        message={
          deleteTarget
            ? t("actions.deleteConfirm", { alias: deleteTarget.alias })
            : ""
        }
        confirmLabel={tCommon("actions.delete")}
        loading={deleteLoading}
        error={deleteError}
      />
    </>
  );
}
