import { Skeleton } from "@/components/ui/skeleton";

export default function Loading() {
  return <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8" aria-busy="true"><Skeleton className="h-8 w-60" /><Skeleton className="h-24 w-full" /><Skeleton className="h-9 w-full" /><Skeleton className="h-60 w-full" /></div>;
}
