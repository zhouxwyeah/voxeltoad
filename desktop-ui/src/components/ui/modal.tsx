import { useCallback, useEffect, useId, useRef } from "react";
import { X } from "lucide-react";
import { cn } from "../../lib/cn";

// Modal — generic overlay dialog with title/body/footer slots.
//
// Pure React + Tailwind, no portal library, no framer-motion. The fixed
// positioning works under both the browser dev server and the Wails webview
// (App form): the webview renders a stable DOM tree, so a top-level fixed
// element covers the full viewport without needing a portal to document.body.
// Mirrors web/src/components/modal.tsx minus the next-intl dependency.
//
// Layout contract: the panel is a fixed-height flex column (max-h-[85vh]);
// title and footer never scroll, body scrolls (design-system.md §3 Modal).

// Size tiers mirror web/src/components/modal.tsx exactly (xl = max-w-2xl).
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
 * in sync if the body padding changes. Mirrors web's modalFormActionsClass.
 */
export const modalFormActionsClass =
  "sticky bottom-0 -mx-6 -mb-4 flex justify-end gap-3 border-t border-border bg-background px-6 pb-4 pt-4";

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

  // Lock body scroll while open (also works in the Wails webview).
  const onKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    },
    [onClose],
  );

  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.style.overflow = prev;
      document.removeEventListener("keydown", onKeyDown);
    };
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
      <div className={cn("flex max-h-[85vh] w-full flex-col rounded-lg border border-border bg-background shadow-lg", sizeClasses[size])}>
        {/* Title */}
        <div className="flex shrink-0 items-center justify-between border-b border-border px-6 py-4">
          <h2 id={titleId} className="text-lg font-semibold text-foreground">
            {title}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            aria-label="关闭"
          >
            <X className="h-4 w-4" strokeWidth={1.6} />
          </button>
        </div>

        {/* Body */}
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">{children}</div>

        {/* Footer */}
        {footer && <div className="flex shrink-0 justify-end gap-3 border-t border-border px-6 py-4">{footer}</div>}
      </div>
    </div>
  );
}
