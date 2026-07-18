"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";
import { Modal } from "@/components/modal";
import { APIKeyForm } from "./create-form";
import { APIKeysTable } from "./table";

type KeyRow = Record<string, unknown>;
type ModelOption = { value: string; label: string };

export function APIKeysPageClient({
  rows,
  nextCursor,
  models,
}: {
  rows: KeyRow[];
  nextCursor: string;
  models: ModelOption[];
}) {
  const t = useTranslations("api-keys");
  const [createOpen, setCreateOpen] = useState(false);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<KeyRow | null>(null);

  const handleCreated = (plaintext?: string) => {
    if (plaintext) setCreatedKey(plaintext);
  };

  return (
    <>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-foreground">
            {t("heading")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("subtitle")}
          </p>
        </div>
        <Button variant="primary" onClick={() => setCreateOpen(true)}>
          {t("actions.create")}
        </Button>
      </div>
      <APIKeysTable
        rows={rows}
        nextCursor={nextCursor}
        onEdit={(row) => {
          setEditTarget(row);
          setEditOpen(true);
        }}
      />
      {createOpen && (
        <CreateModalPanel
          models={models}
          onClose={() => {
            setCreateOpen(false);
            setCreatedKey(null);
          }}
          onCreated={handleCreated}
          createdKey={createdKey}
        />
      )}
      {editTarget && (
        <Modal
          open={editOpen}
          onClose={() => {
            setEditOpen(false);
            setEditTarget(null);
          }}
          title={t("modal.editTitle")}
          size="md"
        >
          <APIKeyForm
            models={models}
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
    </>
  );
}

/**
 * CreateModalPanel is a self-contained modal that shows the create form, and
 * on success transitions to the one-time plaintext reveal modal.
 */
function CreateModalPanel({
  onClose,
  onCreated,
  createdKey,
  models,
}: {
  onClose: () => void;
  onCreated: (plaintext?: string) => void;
  createdKey: string | null;
  models: ModelOption[];
}) {
  const t = useTranslations("api-keys");
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const tCommon = useTranslations("common");

  if (createdKey !== null) {
    return (
      <Modal
        open
        onClose={() => {}}
        title={t("modal.keyCreated")}
        size="sm"
        footer={
          <Button variant="primary" onClick={onClose}>
            {t("plaintext.saved")}
          </Button>
        }
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-destructive font-medium">
            {t("plaintext.warning")}
          </p>
          <div className="flex items-center gap-2 rounded-md border border-border bg-muted p-3">
            <code className="flex-1 break-all text-sm text-foreground font-mono">
              {createdKey}
            </code>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                navigator.clipboard.writeText(createdKey);
              }}
            >
              {t("plaintext.copy")}
            </Button>
          </div>
        </div>
      </Modal>
    );
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={t("modal.createTitle")}
      size="md"
    >
      <APIKeyForm models={models} onCancel={onClose} onSuccess={onCreated} />
    </Modal>
  );
}
