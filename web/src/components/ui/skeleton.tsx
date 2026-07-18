import { cn } from "@/lib/utils"

/**
 * Skeleton primitive (design-system.md §5, §8).
 *
 * Pure Tailwind animate-pulse placeholder. Use inside loading.tsx route
 * skeletons or anywhere a content block is loading. Compose by sizing with
 * className (e.g. `<Skeleton className="h-4 w-24" />`).
 */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={cn("animate-pulse rounded-md bg-muted", className)}
    />
  )
}
