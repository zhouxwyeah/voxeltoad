/**
 * Time-range presets for the admin overview page.
 *
 * The UI exposes 5 fixed ranges (今天/昨天/本周/本月/上月) as a button group.
 * The selected preset is resolved to UTC RFC3339 `from`/`to` here and passed
 * to the backend; the backend stays timezone-agnostic.
 *
 * All ranges are computed in the browser's local timezone (per product
 * decision: users interpret "今天" as their local day, not UTC). Conversion to
 * UTC happens via Date.toISOString() at the boundary.
 */

export type OverviewRange =
  | "today"
  | "yesterday"
  | "week"
  | "month"
  | "last_month";

export const RANGE_OPTIONS: OverviewRange[] = [
  "today",
  "yesterday",
  "week",
  "month",
  "last_month",
];

/**
 * Resolve a preset to an optional {from, to} pair in UTC RFC3339.
 * - `from` is inclusive lower bound (created_at >= from)
 * - `to` is exclusive upper bound (created_at < to)
 * - Absent fields mean unbounded in that direction.
 */
export function rangeToFromTo(
  range: string,
): { from?: string; to?: string } {
  const now = new Date();

  // Local midnight of the given date (mutates a copy).
  const localMidnight = (d: Date): Date => {
    const x = new Date(d);
    x.setHours(0, 0, 0, 0);
    return x;
  };

  // Local midnight of d, as UTC ISO string.
  const startOfDayUTC = (d: Date): string => localMidnight(d).toISOString();

  switch (range) {
    case "yesterday": {
      const yesterday = new Date(now);
      yesterday.setDate(yesterday.getDate() - 1);
      return { from: startOfDayUTC(yesterday), to: startOfDayUTC(now) };
    }
    case "week": {
      // 本周 = Monday 00:00 local → now. getDay(): 0=Sun, 1=Mon, ...
      // Convert so Monday=0: (getDay() + 6) % 7.
      const dayIdx = (now.getDay() + 6) % 7;
      const monday = new Date(now);
      monday.setDate(now.getDate() - dayIdx);
      return { from: startOfDayUTC(monday) };
    }
    case "month": {
      // 本月 = 1st of current month 00:00 local → now.
      const first = new Date(now.getFullYear(), now.getMonth(), 1);
      return { from: first.toISOString() };
    }
    case "last_month": {
      // 上月 = 1st of previous month 00:00 local → 1st of current month 00:00 local.
      const firstLast = new Date(now.getFullYear(), now.getMonth() - 1, 1);
      const firstThis = new Date(now.getFullYear(), now.getMonth(), 1);
      return { from: firstLast.toISOString(), to: firstThis.toISOString() };
    }
    case "today":
    default:
      return { from: startOfDayUTC(now) };
  }
}
