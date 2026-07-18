import { Skeleton } from "@/components/ui/skeleton";

/**
 * Route-level loading skeleton for the request-logs page (design-system.md §5).
 * Mirrors the page layout (max-w-7xl wide table).
 */
export default function Loading() {
  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-6 p-8">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-4 w-72" />
      </div>

      <div className="overflow-hidden rounded-lg border border-border">
        <div className="divide-y divide-border">
          {Array.from({ length: 10 }).map((_, i) => (
            <div key={i} className="flex gap-4 p-3">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-4 w-24" />
              <Skeleton className="h-4 flex-1" />
              <Skeleton className="h-4 w-20" />
              <Skeleton className="h-4 w-16" />
              <Skeleton className="h-4 w-24" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
