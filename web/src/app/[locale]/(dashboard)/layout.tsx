import { getLocale, getTranslations } from "next-intl/server";
import { redirect } from "@/i18n/navigation";
import { getSession } from "@/lib/session";
import { has } from "@/lib/permissions";
import { NAV_PERMS } from "@/lib/nav-perms";
import { NavLink } from "@/components/nav-link";
import { ChangePasswordButton } from "@/components/change-password-button";
import { Toaster } from "@/components/ui/toaster";
import { logoutAction } from "./actions";

// Every dashboard route reads the session cookie per request — never prerender.
export const dynamic = "force-dynamic";

/**
 * Dashboard layout + auth guard + role-based navigation (design/frontend.md §5).
 * Any route under (dashboard) requires a valid session token and a known
 * operator role. Without either we bounce to /login. The nav sidebar is gated
 * by role: super-admin sees global config; tenant-admin sees tenant-scoped
 * resources. The real boundary remains the admin API's 403.
 */
export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const session = await getSession();
  if (!session.token || !session.role) {
    const locale = await getLocale();
    redirect({ href: "/login", locale });
  }

  const t = await getTranslations("common");

  return (
    <div className="flex min-h-full flex-1">
      <aside className="flex w-60 shrink-0 flex-col border-r border-border bg-muted">
        <div className="flex items-center gap-2 px-5 py-5">
          {/* Brand mark: a small blue rounded square (gateway glyph). */}
          <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <svg
              viewBox="0 0 16 16"
              className="h-4 w-4"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="M2 4h12M2 8h12M2 12h8" />
            </svg>
          </span>
          <div className="flex flex-col leading-tight">
            <span className="text-sm font-semibold text-foreground">
              {t("appName")}
            </span>
            <span className="text-[11px] text-muted-foreground">
              {t("appSubtitle")}
            </span>
          </div>
        </div>
        <nav className="flex flex-1 flex-col gap-0.5 px-3 py-2">
          {/* Global config: visible only to global-scope operators holding the
              permission (structural gate on scopeKind, mirroring the
              backend's global-vs-tenant split — a future custom tenant role
              must not surface global nav items even if misconfigured with a
              global permission key). */}
          {session.scopeKind === "global" && (
            <>
              {has(session, NAV_PERMS.overview) && (
                <NavLink href="/overview">{t("nav.overview")}</NavLink>
              )}
              {has(session, NAV_PERMS.provider) && (
                <>
                  <NavLink href="/providers">{t("nav.providers")}</NavLink>
                  <NavLink href="/models">{t("nav.models")}</NavLink>
                  <NavLink href="/routes">{t("nav.routes")}</NavLink>
                  <NavLink href="/plugins">{t("nav.plugins")}</NavLink>
                </>
              )}
              {has(session, NAV_PERMS.tenant) && (
                <NavLink href="/tenants">{t("nav.tenants")}</NavLink>
              )}
              {has(session, NAV_PERMS.operator) && (
                <NavLink href="/operators">{t("nav.operators")}</NavLink>
              )}
              {has(session, NAV_PERMS.role) && (
                <NavLink href="/roles">{t("nav.roles")}</NavLink>
              )}
              {has(session, NAV_PERMS.configHistory) && (
                <NavLink href="/config/history">{t("nav.configHistory")}</NavLink>
              )}
              {has(session, NAV_PERMS.settings) && (
                <NavLink href="/settings">{t("nav.settings")}</NavLink>
              )}
              {has(session, NAV_PERMS.dataplane) && (
                <NavLink href="/data-plane-nodes">{t("nav.dataPlaneNodes")}</NavLink>
              )}
            </>
          )}
          {/* Tenant-scoped: visible only to tenant-scope operators (structural
              gate on scopeKind, mirroring the backend's requireTenantScoped()
              — NOT permission-only, since a global-scope wildcard role like
              super-admin would otherwise satisfy any has(perm) check and leak
              tenant-only nav items). */}
          {session.scopeKind === "tenant" && has(session, NAV_PERMS.apiKey) && (
            <>
              <NavLink href="/api-keys">{t("nav.apiKeys")}</NavLink>
              <NavLink href="/groups">{t("nav.groups")}</NavLink>
              <NavLink href="/quotas">{t("nav.quotas")}</NavLink>
            </>
          )}
          {/* Both-scope: available to any authenticated operator. */}
          <div className="mt-2 flex flex-col gap-0.5 border-t border-border pt-2">
            <NavLink href="/model-catalog">{t("nav.modelCatalog")}</NavLink>
            {has(session, NAV_PERMS.usage) && (
              <NavLink href="/usage">{t("nav.usage")}</NavLink>
            )}
            {has(session, NAV_PERMS.audit) && (
              <NavLink href="/audit">{t("nav.audit")}</NavLink>
            )}
            {has(session, NAV_PERMS.requestLog) && (
              <NavLink href="/request-logs">{t("nav.requestLogs")}</NavLink>
            )}
            {has(session, NAV_PERMS.requestLog) && (
              <NavLink href="/trace">{t("nav.trace")}</NavLink>
            )}
          </div>
        </nav>
        <div className="border-t border-border px-3 py-3">
          <ChangePasswordButton />
          <form action={logoutAction}>
            <button
              type="submit"
              className="rounded-md px-3 py-1.5 text-left text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            >
              {t("actions.signOut")}
            </button>
          </form>
        </div>
      </aside>
      <main className="flex-1 bg-background">{children}</main>
      <Toaster />
    </div>
  );
}
