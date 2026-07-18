import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// Mirrors the admin UI's web/src/lib/utils.ts cn() helper.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
