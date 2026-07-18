import { describe, expect, it } from "vitest";
import {
  agentLabel,
  agentTone,
  displayToMicro,
  formatDuration,
  formatDurationCompact,
  formatNumber,
  formatTokens,
  microToDisplay,
  shortId,
  statusTone,
} from "./format";

describe("agentLabel", () => {
  it("maps known agent types to display labels", () => {
    expect(agentLabel("claude-code")).toBe("Claude Code");
    expect(agentLabel("codex")).toBe("Codex");
    expect(agentLabel("codebuddy")).toBe("CodeBuddy");
    expect(agentLabel("workbuddy")).toBe("WorkBuddy");
    expect(agentLabel("opencode")).toBe("OpenCode");
  });

  it("returns '其他' for empty agent type", () => {
    expect(agentLabel("")).toBe("其他");
  });

  it("passes through unknown agent types unchanged", () => {
    expect(agentLabel("some-new-agent")).toBe("some-new-agent");
  });
});

describe("agentTone", () => {
  it("returns the configured tone for known types", () => {
    expect(agentTone("claude-code")).toBe("primary");
    expect(agentTone("codebuddy")).toBe("success");
    expect(agentTone("codex")).toBe("warning");
  });

  it("falls back to muted for unknown / empty", () => {
    expect(agentTone("unknown")).toBe("muted");
    expect(agentTone("")).toBe("muted");
  });
});

describe("formatNumber", () => {
  it("formats undefined as 0 in zh-CN", () => {
    expect(formatNumber(undefined)).toBe("0");
  });

  it("formats thousands with locale separators", () => {
    // zh-CN uses , as the thousands separator.
    expect(formatNumber(1234567)).toBe("1,234,567");
  });
});

describe("formatDuration", () => {
  it("renders sub-second durations in ms", () => {
    expect(formatDuration(500)).toBe("500ms");
    expect(formatDuration(999)).toBe("999ms");
  });

  it("renders >=1s durations in seconds with 2 decimals", () => {
    expect(formatDuration(1000)).toBe("1.00s");
    expect(formatDuration(2340)).toBe("2.34s");
  });

  it("treats undefined as 0ms", () => {
    expect(formatDuration(undefined)).toBe("0ms");
  });
});

describe("shortId", () => {
  it("returns '-' for empty / undefined", () => {
    expect(shortId("")).toBe("-");
    expect(shortId(undefined)).toBe("-");
  });

  it("truncates ids longer than n chars with ellipsis", () => {
    expect(shortId("abcdefghijklmnop", 5)).toBe("abcde…");
  });

  it("returns short ids unchanged", () => {
    expect(shortId("abc", 10)).toBe("abc");
  });
});

describe("statusTone", () => {
  it("maps 2xx to success", () => {
    expect(statusTone(200)).toBe("success");
    expect(statusTone(204)).toBe("success");
  });

  it("maps 4xx to warning", () => {
    expect(statusTone(400)).toBe("warning");
    expect(statusTone(404)).toBe("warning");
  });

  it("maps 5xx to destructive", () => {
    expect(statusTone(500)).toBe("destructive");
    expect(statusTone(503)).toBe("destructive");
  });

  it("maps undefined / 0 to muted", () => {
    expect(statusTone(undefined)).toBe("muted");
    expect(statusTone(0)).toBe("muted");
  });
});

describe("formatDurationCompact", () => {
  it("renders sub-minute durations as seconds with 1 decimal", () => {
    expect(formatDurationCompact(800)).toBe("0.8s");
    expect(formatDurationCompact(17586)).toBe("17.6s");
  });

  it("renders >=1m durations as m+s", () => {
    expect(formatDurationCompact(120000)).toBe("2m0s");
    expect(formatDurationCompact(123000)).toBe("2m3s");
  });

  it("renders 0 / undefined as —", () => {
    expect(formatDurationCompact(0)).toBe("—");
    expect(formatDurationCompact(undefined)).toBe("—");
  });
});

describe("formatTokens", () => {
  it("renders small counts as-is", () => {
    expect(formatTokens(999)).toBe("999");
  });

  it("renders thousands with k suffix", () => {
    expect(formatTokens(11300)).toBe("11.3k");
  });

  it("renders millions with M suffix", () => {
    expect(formatTokens(2_500_000)).toBe("2.5M");
  });
});

describe("microToDisplay", () => {
  it("converts micro-units to a 2-decimal string", () => {
    expect(microToDisplay(2_500_000)).toBe("2.50");
    expect(microToDisplay(0)).toBe("0.00");
    expect(microToDisplay(10_000)).toBe("0.01");
  });

  it("rounds half-up on the fractional remainder", () => {
    expect(microToDisplay(1_005_000)).toBe("1.01");
  });
});

describe("displayToMicro", () => {
  it("parses decimal strings into micro-units", () => {
    expect(displayToMicro("2.5")).toBe(2_500_000);
    expect(displayToMicro("12")).toBe(12_000_000);
    expect(displayToMicro("0.01")).toBe(10_000);
  });

  it("throws RangeError on malformed input", () => {
    expect(() => displayToMicro("abc")).toThrow(RangeError);
    expect(() => displayToMicro("")).toThrow(RangeError);
  });
});
