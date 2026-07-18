import { describe, expect, it } from "vitest";
import {
  agentLabel,
  agentTone,
  formatDuration,
  formatNumber,
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
