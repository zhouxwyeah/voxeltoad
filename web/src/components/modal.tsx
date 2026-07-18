"use client";

import { useCallback, useEffect, useId, useRef } from "react";
import { useTranslations } from "next-intl";
import { X } from "lucide-react";
import { Button } from "@/components/ui";

/*
 * Modal and ConfirmModal (design-system.md §3).
 *
 * Pure React + Tailwind, no portal library, no framer-motion.
 * Modal: generic overlay dialog with title/body/footer slots.
 * ConfirmModal: thin wrapper for delete/dangerous action confirmation.
 *
 * Layout contract: the panel is a fixed-height flex column (max-h-[85vh]);
 * title and footer never scroll, body scrolls. Form modals keep their action
 * row visible via a sticky footer bar inside the form (see design-system.md
 * §3 Modal / §4.3) — pending/submit state stays co-located with the form.
 */

const sizeClasses = {
  sm: "max-w-sm",
  md: "max-w-md",
  lg: "max-w-lg",
  xl: "max-w-2xl",
};

/**
 * Action row for forms rendered inside a Modal body (design-system.md §3):
 * sticky footer bar pinned to the bottom of the visible area while fields
 * scroll beneath. The negative margins are coupled to the Modal body padding
 * (px-6 py-4) so the hairline and background span edge to edge — keep them
 * in sync if the body padding changes.
 */
export const modalFormActionsClass =
  "sticky bottom-0 -mx-6 -mb-4 flex justify-end gap-3 border-t border-border bg-background px-6 pb-4 pt-4";

/* ------------------------------------------------------------------ */
/*  Modal                                                             */
/* ------------------------------------------------------------------ */

export function Modal({
  open,
  onClose,
  title,
  size = "md",
  children,
  footer,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  size?: "sm" | "md" | "lg" | "xl";
  children: React.ReactNode;
  footer?: React.ReactNode;
}) {
  const titleId = useId();
  const backdropRef = useRef<HTMLDivElement>(null);

  // Lock body scroll while open
  useEffect(() => {
    if (open) {
      const prev = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      return () => {
        document.body.style.overflow = prev;
      };
    }
  }, [open]);

  // Close on Escape
  const onKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    },
    [onClose],
  );

  useEffect(() => {
    if (open) {
      document.addEventListener("keydown", onKeyDown);
      return () => document.removeEventListener("keydown", onKeyDown);
    }
  }, [open, onKeyDown]);

  if (!open) return null;

  return (
    <div
      ref={backdropRef}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={(e) => {
        if (e.target === backdropRef.current) onClose();
      }}
      role="dialog"
      aria-labelledby={titleId}
      aria-modal="true"
    >
      <div
        className={`flex max-h-[85vh] w-full ${sizeClasses[size]} flex-col rounded-lg border border-border bg-background shadow-lg`}
      >
        {/* Title */}
        <div className="flex shrink-0 items-center justify-between border-b border-border px-6 py-4">
          <h2
            id={titleId}
            className="text-lg font-semibold text-foreground"
          >
            {title}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" strokeWidth={1.6} />
          </button>
        </div>

        {/* Body */}
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
          {children}
        </div>

        {/* Footer */}
        {footer && (
          <div className="flex shrink-0 justify-end gap-3 border-t border-border px-6 py-4">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/*  ConfirmModal                                                      */
/* ------------------------------------------------------------------ */

export function ConfirmModal({
  open,
  onCancel,
  onConfirm,
  title,
  message,
  confirmLabel,
  loadingLabel,
  loading = false,
  error,
}: {
  open: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  title: string;
  message: string;
  confirmLabel?: string;
  /** Label while `loading` — defaults to common "Deleting…". */
  loadingLabel?: string;
  loading?: boolean;
  error?: string | null;
}) {
  const t = useTranslations("common");
  return (
    <Modal
      open={open}
      onClose={onCancel}
      title={title}
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={onCancel}>
            {t("actions.cancel")}
          </Button>
          <Button
            variant="destructive"
            onClick={onConfirm}
            disabled={loading}
          >
            {loading
              ? (loadingLabel ?? t("actions.deleting"))
              : (confirmLabel ?? t("actions.delete"))}
          </Button>
        </>
      }
    >
      <p className="text-sm text-muted-foreground">{message}</p>
      {error && (
        <p
          role="alert"
          className="mt-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
    </Modal>
  );
}
