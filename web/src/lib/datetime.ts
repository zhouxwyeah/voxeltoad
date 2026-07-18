/**
 * Bidirectional helpers for `<input type="datetime-local">` values and
 * RFC3339/ISO 8601 UTC timestamps used in the admin API query string.
 *
 * datetime-local inputs speak `YYYY-MM-DDTHH:mm` in the browser's local
 * timezone. The admin backend expects full RFC3339 timestamps (usually UTC).
 * These helpers keep the UI in local time while the URL/backend stay in UTC.
 */

/** Convert an RFC3339/ISO 8601 timestamp to `YYYY-MM-DDTHH:mm` local time. */
export function rfc3339ToLocalDatetime(value: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const year = d.getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  const hours = String(d.getHours()).padStart(2, "0");
  const minutes = String(d.getMinutes()).padStart(2, "0");
  return `${year}-${month}-${day}T${hours}:${minutes}`;
}

/** Convert a datetime-local value (local time) to an RFC3339 UTC timestamp. */
export function localDatetimeToRfc3339(value: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return d.toISOString();
}
