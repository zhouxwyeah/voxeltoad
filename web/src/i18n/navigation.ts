import { createNavigation } from "next-intl/navigation";
import { routing } from "./routing";

// Locale-aware navigation helpers (use in client/server components instead
// of next/navigation when locale is needed in the path).
//
// This lives in a separate module from routing.ts so that the Edge Runtime
// middleware — which only needs the routing config — does not transitively
// pull in next-intl/navigation → next-intl/server → request.ts (which uses
// node:fs/node:path to discover namespaces, unsupported in Edge Runtime).
export const { Link, redirect, usePathname, useRouter } =
  createNavigation(routing);
