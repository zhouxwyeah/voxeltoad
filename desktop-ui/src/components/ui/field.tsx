import { cloneElement, isValidElement, useId } from "react";
import { cn } from "../../lib/cn";

// Field — uniform wrapper around a form control: label + required marker +
// optional hint/error + optional unit suffix.
//
// Use in place of ad-hoc `<label className="text-sm">…<Input/></label>`
// blocks. See design/desktop.md §12.

export function Field({
  label,
  required,
  hint,
  error,
  suffix,
  full,
  className,
  children,
}: {
  label: string;
  /** Show red `*` after label. Purely visual; validation still lives in the caller. */
  required?: boolean;
  /** Muted helper text shown under the control when no error. */
  hint?: string;
  /** Error message. When set, replaces `hint` and paints the label red. */
  error?: string;
  /** Right-aligned unit suffix next to the control, e.g. "s" or "/ 1M tokens". */
  suffix?: string;
  /** Span all grid columns (equivalent to `sm:col-span-2`). */
  full?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  const id = useId();
  const describedBy = error ? `${id}-err` : hint ? `${id}-hint` : undefined;

  // Wire label htmlFor → control id when the child is a single form element.
  const control = isValidElement(children)
    ? cloneElement(children as React.ReactElement<Record<string, unknown>>, {
        id,
        "aria-describedby": describedBy,
      })
    : children;

  return (
    <div className={cn("flex flex-col gap-1", full && "sm:col-span-2", className)}>
      <label htmlFor={id} className={cn("text-sm font-medium", error ? "text-destructive" : "text-foreground")}>
        {label}
        {required && <span className="ml-0.5 text-destructive">*</span>}
      </label>
      <div className="flex items-center gap-2">
        <div className="flex-1">{control}</div>
        {suffix && <span className="shrink-0 text-xs text-muted-foreground">{suffix}</span>}
      </div>
      {error ? (
        <p id={`${id}-err`} className="text-xs text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  );
}
