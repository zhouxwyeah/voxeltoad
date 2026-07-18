import type { ReactNode } from "react";

/**
 * EmptyState primitive (design-system.md §6).
 *
 * Visual template for empty lists: icon + title + optional description + optional CTA.
 * Use inside table bodies or as a standalone page placeholder.
 */
export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon?: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-4 py-10 text-center">
      {icon && (
        <div className="text-muted-foreground">{icon}</div>
      )}
      <p className="text-sm font-medium text-foreground">{title}</p>
      {description && (
        <p className="max-w-xs text-xs text-muted-foreground">{description}</p>
      )}
      {action && <div className="mt-1">{action}</div>}
    </div>
  );
}
