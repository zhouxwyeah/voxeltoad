"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { Link } from "@/i18n/navigation";
import { Button, Input } from "@/components/ui";
import { EmptyState } from "@/components/ui/empty-state";
import { Modal, ConfirmModal } from "@/components/modal";
import { ModelCard, type CatalogModel } from "./model-card";
import { ModelForm } from "./create-form";
import { deleteModel } from "./actions";

type ProviderOption = { name: string };

/**
 * Models page: card grid browsable by all authenticated operators, with
 * optional CRUD for super-admin (canWrite=true). Filtering (search +
 * capability) is URL-driven for shareable/bookmarkable views, following the
 * convention used by request-logs/usage.
 *
 * Merged from the old /models (table + CRUD) and /model-catalog (cards +
 * read-only) per ADR-0044.
 */
export function ModelsPageClient({
  models,
  providers,
  query,
  capability,
  canWrite,
}: {
  models: CatalogModel[];
  providers: ProviderOption[];
  query: string;
  capability: string;
  canWrite: boolean;
}) {
  const t = useTranslations("models");
  const tCommon = useTranslations("common");
  const router = useRouter();
  const searchParams = useSearchParams();
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<CatalogModel | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<CatalogModel | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const hasProviders = providers.length > 0;

  // Collect the union of all capabilities across models for the filter chips.
  const allCapabilities = useMemo(() => {
    const set = new Set<string>();
    for (const m of models) {
      for (const c of m.capabilities ?? []) set.add(c);
    }
    return Array.from(set).sort();
  }, [models]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return models.filter((m) => {
      if (capability && !(m.capabilities ?? []).includes(capability)) return false;
      if (!q) return true;
      const haystack = [m.alias, m.description ?? "", ...(m.tags ?? [])]
        .join(" ")
        .toLowerCase();
      return haystack.includes(q);
    });
  }, [models, query, capability]);

  function applyParam(key: string, value: string) {
    const params = new URLSearchParams(searchParams.toString());
    if (value) params.set(key, value);
    else params.delete(key);
    router.push(`/models?${params.toString()}`);
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setDeleteError(null);
    const res = await deleteModel(deleteTarget.alias);
    if (res.ok) {
      setDeleteTarget(null);
      router.refresh();
    } else {
      setDeleteError(res.error);
    }
    setDeleteLoading(false);
  }

  return (
    <>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t("subtitle")}</p>
        </div>
        {canWrite && hasProviders && (
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            {t("actions.create")}
          </Button>
        )}
      </div>

      {/* super-admin without providers yet: guide them to create one first. */}
      {canWrite && !hasProviders && (
        <div className="flex items-center justify-between rounded-md border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
          <span>{t("actions.noProviders")}</span>
          <Button href="/providers" variant="outline" size="sm">
            {t("actions.goToProviders")}
          </Button>
        </div>
      )}

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex-1">
          <Input
            type="search"
            placeholder={t("search.placeholder")}
            defaultValue={query}
            onChange={(e) => applyParam("q", e.target.value)}
          />
        </div>
        {allCapabilities.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-muted-foreground">
              {t("filters.capability")}:
            </span>
            <button
              type="button"
              onClick={() => applyParam("capability", "")}
              className={`rounded-full px-2.5 py-0.5 text-xs transition-colors ${
                !capability
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-foreground hover:bg-accent"
              }`}
            >
              {t("filters.allCapabilities")}
            </button>
            {allCapabilities.map((c) => {
              const known: Record<string, string> = {
                vision: t("capabilities.vision"),
                function_calling: t("capabilities.function_calling"),
                streaming: t("capabilities.streaming"),
                code: t("capabilities.code"),
              };
              return (
                <button
                  key={c}
                  type="button"
                  onClick={() => applyParam("capability", c)}
                  className={`rounded-full px-2.5 py-0.5 text-xs transition-colors ${
                    capability === c
                      ? "bg-primary text-primary-foreground"
                      : "bg-muted text-foreground hover:bg-accent"
                  }`}
                >
                  {known[c] ?? c}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {filtered.length === 0 ? (
        <EmptyState title={t("empty")} />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {filtered.map((m) => (
            <Link
              key={m.alias}
              href={`/models/${encodeURIComponent(m.alias)}`}
              className="block"
            >
              <ModelCard
                model={m}
                onEdit={
                  canWrite
                    ? () => {
                        setEditTarget(m);
                        setEditOpen(true);
                      }
                    : undefined
                }
                onDelete={canWrite ? () => setDeleteTarget(m) : undefined}
              />
            </Link>
          ))}
        </div>
      )}

      {/* Create Modal (super-admin only — button only renders when canWrite && hasProviders) */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t("modal.createTitle")}
        size="xl"
      >
        <ModelForm
          providers={providers}
          onCancel={() => setCreateOpen(false)}
          onSuccess={() => setCreateOpen(false)}
        />
      </Modal>

      {/* Edit Modal (super-admin only) */}
      {editTarget && (
        <Modal
          open={editOpen}
          onClose={() => {
            setEditOpen(false);
            setEditTarget(null);
          }}
          title={t("modal.editTitle")}
          size="xl"
        >
          <ModelForm
            providers={providers}
            defaultValues={editTarget}
            onCancel={() => {
              setEditOpen(false);
              setEditTarget(null);
            }}
            onSuccess={() => {
              setEditOpen(false);
              setEditTarget(null);
            }}
          />
        </Modal>
      )}

      {/* Delete ConfirmModal (super-admin only) */}
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

      <p className="sr-only">{tCommon("appName")}</p>
    </>
  );
}
