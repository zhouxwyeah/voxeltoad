import { Link, useLocation } from "react-router-dom";
import { cn } from "../../lib/cn";

// Sidebar — mirrors the admin dashboard sidebar (web/src/app/[locale]/
// (dashboard)/layout.tsx): w-60 muted rail, logo + two-line brand block,
// text-only nav links with accent active state, and a divider separating
// config sections from observability sections. Desktop keeps its own brand
// name and stays Chinese-only (no i18n framework).
const configItems = [
  { to: "/", label: "概览", match: (p: string) => p === "/" },
  { to: "/providers", label: "供应商", match: sectionMatch("/providers") },
  { to: "/models", label: "模型", match: sectionMatch("/models") },
  { to: "/routes", label: "路由", match: sectionMatch("/routes") },
];

const observeItems = [
  // The trace viewer lives under /trace/:sessionId but belongs to the
  // sessions section (mirrors admin, where /trace/* highlights the trace nav).
  {
    to: "/sessions",
    label: "会话浏览器",
    match: (p: string) => sectionMatch("/sessions")(p) || sectionMatch("/trace")(p),
  },
];

/** Section-root match: /providers and /providers/:anything both highlight. */
function sectionMatch(href: string) {
  return (pathname: string) => pathname === href || pathname.startsWith(`${href}/`);
}

function SideNavLink({
  to,
  label,
  match,
}: {
  to: string;
  label: string;
  match: (pathname: string) => boolean;
}) {
  const { pathname } = useLocation();
  const active = match(pathname);
  return (
    <Link
      to={to}
      className={cn(
        "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
        active
          ? "bg-accent text-accent-foreground"
          : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
      )}
    >
      {label}
    </Link>
  );
}

export function Sidebar() {
  return (
    <aside className="flex w-60 shrink-0 flex-col border-r border-border bg-muted">
      {/* Brand mark: same logo + name/subtitle block as the admin console. */}
      <div className="flex items-center gap-2 px-5 py-5">
        <img src="/logo.svg" alt="voxeltoad" className="h-8 w-8" />
        <div className="flex flex-col leading-tight">
          <span className="text-sm font-semibold text-foreground">桌面网关助手</span>
          <span className="text-[11px] text-muted-foreground">voxeltoad</span>
        </div>
      </div>
      <nav className="flex flex-1 flex-col gap-0.5 px-3 py-2">
        {configItems.map((it) => (
          <SideNavLink key={it.to} {...it} />
        ))}
        <div className="mt-2 flex flex-col gap-0.5 border-t border-border pt-2">
          {observeItems.map((it) => (
            <SideNavLink key={it.to} {...it} />
          ))}
        </div>
      </nav>
      <div className="border-t border-border px-3 py-3">
        <span className="px-3 text-xs text-muted-foreground">本地代理 · 单用户</span>
      </div>
    </aside>
  );
}
