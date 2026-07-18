/**
 * Centralized navigation permission constants.
 *
 * All hardcoded permission strings used in nav gating must live here so that
 * `scripts/check-frontend-permissions.sh` can grep them reliably and diff
 * against the backend catalog in `internal/authz/permission.go`.
 */

export const NAV_PERMS = {
  // Global-scope
  overview: "overview.read",
  provider: "provider.read",
  tenant: "tenant.read",
  operator: "operator.read",
  role: "role.read",
  configHistory: "config_history.read",
  settings: "settings.read",
  dataplane: "dataplane.read",
  // Tenant-scope
  apiKey: "api_key.read",
  // Both-scope
  usage: "usage.read",
  audit: "audit.read",
  requestLog: "request_log.read",
} as const;

export type NavPerm = (typeof NAV_PERMS)[keyof typeof NAV_PERMS];
