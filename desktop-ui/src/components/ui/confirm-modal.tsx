import { useState } from "react";
import { Button } from "./button";
import { Modal } from "./modal";

// ConfirmModal — thin wrapper over Modal for delete/dangerous action
// confirmation (design-system.md §3). Mirrors web's ConfirmModal minus
// next-intl: the desktop UI is Chinese-only for now.
export function ConfirmModal({
  open,
  onCancel,
  onConfirm,
  title,
  message,
  confirmLabel = "删除",
}: {
  open: boolean;
  onCancel: () => void;
  /** Async errors are caught and rendered inline; on success the modal closes. */
  onConfirm: () => Promise<void> | void;
  title: string;
  message: string;
  confirmLabel?: string;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleConfirm() {
    setPending(true);
    setError(null);
    try {
      await onConfirm();
      onCancel();
    } catch (e) {
      setError(String((e as Error)?.message ?? e));
    } finally {
      setPending(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onCancel}
      title={title}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onCancel} disabled={pending}>
            取消
          </Button>
          <Button variant="destructive" onClick={handleConfirm} disabled={pending}>
            {pending ? "删除中…" : confirmLabel}
          </Button>
        </>
      }
    >
      <p className="text-sm text-muted-foreground">{message}</p>
      {error && (
        <p role="alert" className="mt-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </Modal>
  );
}
