import { useState } from "react";
import { cn } from "../../lib/cn";

// Tabs — lightweight tab strip matching the desktop-ui design tokens.
// Uncontrolled by default; pass `value` + `onValueChange` to control.
//
// Follows the same style conventions as select.tsx / modal.tsx (pure React
// + Tailwind, no portal, no framer-motion).

export interface TabItem {
  value: string;
  label: React.ReactNode;
  disabled?: boolean;
}

export function Tabs({
  items,
  defaultValue,
  value,
  onValueChange,
  className,
}: {
  items: TabItem[];
  defaultValue?: string;
  value?: string;
  onValueChange?: (v: string) => void;
  className?: string;
}) {
  const [internal, setInternal] = useState<string>(defaultValue ?? items[0]?.value ?? "");
  const active = value !== undefined ? value : internal;

  const handleClick = (v: string, disabled?: boolean) => {
    if (disabled) return;
    if (value === undefined) setInternal(v);
    onValueChange?.(v);
  };

  return (
    <div role="tablist" className={cn("flex items-center gap-1 border-b border-border", className)}>
      {items.map((it) => {
        const isActive = it.value === active;
        return (
          <button
            key={it.value}
            type="button"
            role="tab"
            aria-selected={isActive}
            aria-disabled={it.disabled || undefined}
            disabled={it.disabled}
            onClick={() => handleClick(it.value, it.disabled)}
            className={cn(
              "relative -mb-px px-3 py-2 text-sm transition-colors",
              "border-b-2 border-transparent text-muted-foreground hover:text-foreground",
              isActive && "border-primary text-foreground font-medium",
              it.disabled && "cursor-not-allowed opacity-50 hover:text-muted-foreground",
            )}
          >
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
