// Display formatting + label maps for the desktop UI (zh-CN).

const AGENT_LABELS: Record<string, string> = {
  "claude-code": "Claude Code",
  codex: "Codex",
  codebuddy: "CodeBuddy",
  workbuddy: "WorkBuddy",
  opencode: "OpenCode",
};

export function agentLabel(agentType: string): string {
  if (!agentType) return "其他";
  return AGENT_LABELS[agentType] ?? agentType;
}

export function agentTone(agentType: string): "primary" | "success" | "warning" | "muted" | "default" {
  switch (agentType) {
    case "claude-code":
      return "primary";
    case "codebuddy":
      return "success";
    case "codex":
      return "warning";
    default:
      return "muted";
  }
}

export function formatNumber(n: number | undefined): string {
  return (n ?? 0).toLocaleString("zh-CN");
}

export function formatDuration(ms: number | undefined): string {
  const v = ms ?? 0;
  if (v < 1000) return `${v}ms`;
  return `${(v / 1000).toFixed(2)}s`;
}

// formatDurationCompact mirrors the admin trace pages' compact duration
// (e.g. 17586 → "17.6s", 800 → "0.8s", 120000 → "2m0s").
export function formatDurationCompact(ms: number | undefined): string {
  const v = ms ?? 0;
  if (v <= 0) return "—";
  if (v < 60_000) return `${(v / 1000).toFixed(1)}s`;
  const m = Math.floor(v / 60_000);
  const s = Math.floor((v % 60_000) / 1000);
  return `${m}m${s}s`;
}

// formatTokens renders an integer token count with a k/M suffix for
// readability in dense table cells (e.g. 11300 → "11.3k"). Mirrors admin.
export function formatTokens(n: number | undefined): string {
  const v = n ?? 0;
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return String(v);
}

export function formatTime(iso: string | undefined): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", { hour12: false });
}

export function shortId(id: string | undefined, n = 10): string {
  if (!id) return "-";
  return id.length > n ? `${id.slice(0, n)}…` : id;
}

export function statusTone(status: number | undefined): "success" | "destructive" | "warning" | "muted" {
  const s = status ?? 0;
  if (s >= 200 && s < 300) return "success";
  if (s >= 400 && s < 500) return "warning";
  if (s >= 500) return "destructive";
  return "muted";
}

/* ---------------------------------------------------------------------- */
/*  Money (ADR-0013) — ported from web/src/lib/money.ts.                  */
/*  All monetary values are int64 micro-units: 1_000_000 micro = 1 unit.  */
/* ---------------------------------------------------------------------- */

export const MICRO_PER_UNIT = 1_000_000;
const DECIMALS = 2;
const SCALE = 10 ** DECIMALS;

/**
 * int64 micro-units → display string with 2 decimal places. Pure integer
 * arithmetic (round-half-up on the fractional remainder) — never divides the
 * raw micro value with floating point.
 */
export function microToDisplay(micro: number): string {
  const negative = micro < 0;
  const abs = Math.abs(micro);
  const whole = Math.floor(abs / MICRO_PER_UNIT);
  const remainder = abs % MICRO_PER_UNIT;
  let frac = Math.floor((remainder * SCALE + MICRO_PER_UNIT / 2) / MICRO_PER_UNIT);
  let wholeOut = whole;
  if (frac >= SCALE) {
    wholeOut += 1;
    frac -= SCALE;
  }
  const fracStr = String(frac).padStart(DECIMALS, "0");
  const sign = negative && (wholeOut !== 0 || frac !== 0) ? "-" : "";
  return `${sign}${wholeOut}.${fracStr}`;
}

/**
 * User-entered decimal string (e.g. "12.5") → int64 micro-units. Uses string
 * concatenation rather than parseFloat(x) * 1e6, avoiding float-multiply
 * precision traps. Inputs with more than 6 fractional digits are truncated.
 * Throws RangeError on malformed input.
 */
export function displayToMicro(input: string): number {
  const trimmed = input.trim();
  const match = /^(-)?(\d+)(?:\.(\d+))?$/.exec(trimmed);
  if (!match) {
    throw new RangeError(`invalid decimal amount: "${input}"`);
  }
  const [, sign, intPart, fracPart = ""] = match;
  const fracDigits = (fracPart + "000000").slice(0, 6);
  const value = Number(intPart + fracDigits);
  return sign ? -value : value;
}
