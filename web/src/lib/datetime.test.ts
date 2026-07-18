import { describe, expect, it } from "vitest";
import {
  localDatetimeToRfc3339,
  rfc3339ToLocalDatetime,
} from "./datetime";

describe("rfc3339ToLocalDatetime", () => {
  it("returns empty string for empty input", () => {
    expect(rfc3339ToLocalDatetime("")).toBe("");
  });

  it("returns empty string for invalid input", () => {
    expect(rfc3339ToLocalDatetime("not-a-date")).toBe("");
  });

  it("converts UTC RFC3339 to local datetime-local format", () => {
    const result = rfc3339ToLocalDatetime("2026-07-05T08:30:00Z");
    expect(result).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
    expect(new Date(result).getTime()).toBe(
      new Date("2026-07-05T08:30:00Z").getTime(),
    );
  });

  it("converts offset RFC3339 to local datetime-local format", () => {
    const result = rfc3339ToLocalDatetime("2026-07-05T16:30:00+08:00");
    expect(result).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
    expect(new Date(result).getTime()).toBe(
      new Date("2026-07-05T16:30:00+08:00").getTime(),
    );
  });
});

describe("localDatetimeToRfc3339", () => {
  it("returns empty string for empty input", () => {
    expect(localDatetimeToRfc3339("")).toBe("");
  });

  it("returns empty string for invalid input", () => {
    expect(localDatetimeToRfc3339("not-a-date")).toBe("");
  });

  it("converts local datetime-local to UTC RFC3339", () => {
    const result = localDatetimeToRfc3339("2026-07-05T08:30");
    expect(result).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
  });

  it("round-trips with rfc3339ToLocalDatetime", () => {
    const local = "2026-07-05T08:30";
    const rfc3339 = localDatetimeToRfc3339(local);
    expect(rfc3339ToLocalDatetime(rfc3339)).toBe(local);
  });
});
