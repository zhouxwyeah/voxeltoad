/**
 * Toast helper (design-system.md §6, §8).
 *
 * Thin re-export of sonner's `toast` so call sites have a single canonical
 * import path (`@/lib/toast`). The <Toaster /> host lives in the dashboard
 * layout; import `toast` from here to fire notifications from any client
 * component.
 *
 *   import { toast } from "@/lib/toast"
 *   toast.success(t("form.successCreated"))
 *   toast.error(tErr(errorKey))
 */
export { toast } from "sonner"
