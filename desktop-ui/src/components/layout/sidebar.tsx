import { NavLink } from "react-router-dom";
import { LayoutDashboard, MessagesSquare, GitBranch, Server, Box, Route } from "lucide-react";
import { cn } from "../../lib/cn";

const items = [
  { to: "/", label: "概览", icon: LayoutDashboard, end: true },
  { to: "/providers", label: "供应商", icon: Server, end: false },
  { to: "/models", label: "模型", icon: Box, end: false },
  { to: "/routes", label: "路由", icon: Route, end: false },
  { to: "/sessions", label: "会话浏览器", icon: GitBranch, end: false },
];

export function Sidebar() {
  return (
    <aside className="flex w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground">
      <div className="flex h-14 items-center gap-2 border-b border-sidebar-border px-4">
        <MessagesSquare className="h-5 w-5 text-primary" />
        <span className="font-semibold">桌面网关助手</span>
      </div>
      <nav className="flex flex-col gap-1 p-2">
        {items.map((it) => (
          <NavLink
            key={it.to}
            to={it.to}
            end={it.end}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "bg-sidebar-accent text-sidebar-accent-foreground"
                  : "text-muted-foreground hover:bg-sidebar-accent/60",
              )
            }
          >
            <it.icon className="h-4 w-4" />
            {it.label}
          </NavLink>
        ))}
      </nav>
      <div className="mt-auto border-t border-sidebar-border p-3 text-xs text-muted-foreground">
        本地代理 · 单用户
      </div>
    </aside>
  );
}
