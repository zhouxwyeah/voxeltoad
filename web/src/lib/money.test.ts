import { describe, expect, it } from "vitest";
import { displayToMicro, microToDisplay, MICRO_PER_UNIT } from "./money";

/**
 * Money module tests (design/frontend.md §6). All monetary values are int64
 * micro-units (ADR-0013: 1_000_000 micro = 1 currency unit; currency is a
 * label only, no per-currency decimal-place rules). We fix 2 decimal places
 * for display/input, matching the backend's "no currency semantics" stance.
 */

describe("MICRO_PER_UNIT", () => {
  it("is 1,000,000 per ADR-0013", () => {
    expect(MICRO_PER_UNIT).toBe(1_000_000);
  });
});

describe("microToDisplay", () => {
  it.each([
    [0, "0.00"],
    [1_000_000, "1.00"],
    [30_000, "0.03"],
    [12_500_000, "12.50"],
    [1, "0.00"], // sub-cent rounds down
    [5_000, "0.01"], // half-cent rounds up (round-half-up, ADR-0013)
    [999_999, "1.00"], // rounds up to next whole unit
    [-1_000_000, "-1.00"],
    [-30_000, "-0.03"],
  ])("microToDisplay(%i) === %s", (micro, expected) => {
    expect(microToDisplay(micro)).toBe(expected);
  });
});

describe("displayToMicro", () => {
  it.each([
    ["0", 0],
    ["1", 1_000_000],
    ["0.03", 30_000],
    ["12.5", 12_500_000],
    ["12.50", 12_500_000],
    ["-1", -1_000_000],
    ["-0.03", -30_000],
    ["0.000001", 1], // 6 decimal places, exact
    ["0.0000001", 0], // 7th decimal truncated
    ["  12.5  ", 12_500_000], // whitespace trimmed
  ])("displayToMicro(%s) === %i", (input, expected) => {
    expect(displayToMicro(input)).toBe(expected);
  });

  it.each(["abc", "", "1.2.3", "-", "1-2", "1.a"])(
    "displayToMicro(%s) throws RangeError",
    (input) => {
      expect(() => displayToMicro(input)).toThrow(RangeError);
    },
  );
});

describe("round-trip", () => {
  it.each([0, 1_000_000, 30_000, 12_500_000, -1_000_000])(
    "displayToMicro(microToDisplay(%i)) === original (within 2-decimal precision)",
    (micro) => {
      expect(displayToMicro(microToDisplay(micro))).toBe(micro);
    },
  );
});
