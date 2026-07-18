import { getTranslations } from "next-intl/server";
import { mapBackendError } from "@/lib/i18n-errors";

/**
 * ForbiddenNotice renders a localized "no permission" region for a back-end 403
 * (design/domain-flows.md §权限不足). It is an async server component: the
 * backend returns the apperr i18n key (e.g. "errors.auth.superAdminRequired")
 * as the error message, which mapBackendError resolves to a path under the
 * "errors" namespace (e.g. "auth.superAdminRequired") for translation here.
 *
 * Render this from an RSC page's catch when handleAdminError returns a
 * ForbiddenOutcome, so a direct-URL hit by an under-privileged operator shows
 * a clean notice instead of an uncaught AdminError.
 */
export async function ForbiddenNotice({ message }: { message: string }) {
  const t = await getTranslations("errors");
  const { key, fallback } = mapBackendError(message);
  const text = key ? t(key) : fallback;

  return (
    <div className="flex items-center gap-3 rounded-md border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
      <svg
        viewBox="0 0 16 16"
        className="h-4 w-4 shrink-0"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M4 7V5a4 4 0 1 1 8 0v2" />
        <rect x="3" y="7" width="10" height="7" rx="1.5" />
      </svg>
      <span>{text}</span>
    </div>
  );
}
