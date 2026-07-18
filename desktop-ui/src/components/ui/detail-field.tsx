import * as React from "react";

// DetailField — read-only label/value pair for detail views (detail Modals,
// detail pages). Mirrors web/src/components/ui.tsx DetailField. Compose inside
// a `flex flex-col gap-4` (single column) or `grid grid-cols-2 gap-x-6 gap-y-4`
// (two-column detail grid).
export function DetailField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="text-sm text-foreground">{children}</span>
    </div>
  );
}
