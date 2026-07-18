"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

/*
 * NavLink — client-only because active-state needs usePathname. The dashboard
 * layout is a server component (it reads the session cookie); this is the one
 * client island in the chrome, for highlighting the current section a la
 * Sentry's sidebar. `active` when the current pathname starts with `href`
 * (section-root match, so /providers/:anything still highlights Providers).
 */
export function NavLink({
  href,
  children,
}: {
  href: string;
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const active =
    pathname === href || pathname.startsWith(`${href}/`);
  return (
    <Link
      href={href}
      className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
        active
          ? "bg-accent text-accent-foreground"
          : "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
      }`}
    >
      {children}
    </Link>
  );
}
