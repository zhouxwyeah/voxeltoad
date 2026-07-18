import * as React from "react";
import { cn } from "../../lib/cn";

// EmptyState — mirrors web's src/components/ui/empty-state.tsx: icon + title +
// optional description + optional CTA. Use inside table bodies or as a
// standalone page placeholder.
export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
  children,
}: {
  icon?: React.ReactNode;
  title: string;
  description?: string;
  action?: React.ReactNode;
  className?: string;
  /** @deprecated prefer `action`; kept for call-site compatibility. */
  children?: React.ReactNode;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-2 px-4 py-10 text-center", className)}>
      {icon && <div className="text-muted-foreground">{icon}</div>}
      <p className="text-sm font-medium text-foreground">{title}</p>
      {description && <p className="max-w-xs text-xs text-muted-foreground">{description}</p>}
      {action && <div className="mt-1">{action}</div>}
      {children}
    </div>
  );
}
