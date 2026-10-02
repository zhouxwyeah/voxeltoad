import { Skeleton } from "@/components/ui/skeleton";

export default function Loading() {
  return <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
    <Skeleton className="h-6 w-40" />
    <Skeleton className="h-9 w-full" />
    <Skeleton className="h-64 w-full" />
  </div>;
}
