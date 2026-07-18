/**
 * Centralized money handling (design/frontend.md §6, mandatory). The backend
 * stores all monetary values (pricing rates, cost, quota balances/deltas) as
 * int64 micro-units — a fixed scaling factor applied to the deployment's one
 * configured currency (ADR-0013: 1_000_000 micro = 1 currency unit; currency
 * is a label only, the backend does no currency-specific semantics —
 * single-currency-per-deployment in phase 1).
 *
 * All amount display/input MUST go through this module — no ad-hoc
 * multiply/divide, which is exactly the float-drift bug class ADR-0013 calls
 * out (new-api's "乘以 10000 存整数" lesson). We fix 2 decimal places for
 * display/input (matching the backend's "no currency semantics" stance —
 * we don't introduce a per-currency decimal-places table like JPY=0/USD=2).
 */

export const MICRO_PER_UNIT = 1_000_000;
const DECIMALS = 2;
const SCALE = 10 ** DECIMALS;

/**
 * int64 micro-units → display string with 2 decimal places. Pure integer
 * arithmetic (round-half-up on the fractional remainder, ADR-0013's stated
 * rounding rule) — never divides the raw micro value with floating point.
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
 * precision traps. Inputs with more than 6 fractional digits are truncated
 * (6 digits already exceeds any real currency's precision needs). Throws
 * RangeError on malformed input.
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
