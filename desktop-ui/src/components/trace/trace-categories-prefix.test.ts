import { describe, expect, it } from "vitest";
import { commonPrefixLength, messagesOf, type TraceDetailLike } from "./trace-categories";

function msg(role: string, content: string): Record<string, unknown> {
  return { role, content };
}

describe("messagesOf", () => {
  it("returns the array when present", () => {
    const d: TraceDetailLike = { messages: [msg("user", "a")] };
    expect(messagesOf(d)).toEqual([msg("user", "a")]);
  });

  it("returns [] for null/undefined/non-array payloads", () => {
    expect(messagesOf(null)).toEqual([]);
    expect(messagesOf(undefined)).toEqual([]);
    expect(messagesOf({})).toEqual([]);
    expect(messagesOf({ messages: null })).toEqual([]);
    expect(messagesOf({ messages: "not-an-array" })).toEqual([]);
  });
});

describe("commonPrefixLength", () => {
  it("returns full length when arrays are identical", () => {
    const a = [msg("system", "s"), msg("user", "u"), msg("assistant", "k")];
    expect(commonPrefixLength(a, a.slice())).toBe(3);
  });

  it("returns the index of the first differing message", () => {
    const a = [msg("system", "s"), msg("user", "u"), msg("assistant", "k")];
    const b = [msg("system", "s"), msg("user", "DIFFERENT"), msg("assistant", "k")];
    expect(commonPrefixLength(a, b)).toBe(1);
  });

  it("returns the full length of the shorter array when it is a prefix of the longer", () => {
    // current is longer than prev (normal growth): prefix = len(prev).
    const prev = [msg("system", "s"), msg("user", "u")];
    const cur = [...prev, msg("assistant", "k"), msg("user", "more")];
    expect(commonPrefixLength(cur, prev)).toBe(2);
  });

  it("returns the full length of the shorter array when current is a true subset of prev", () => {
    // Edge case after client-side history compaction: current holds fewer
    // messages than prev but they still match. Prefix = len(current).
    const cur = [msg("system", "s"), msg("user", "u")];
    const prev = [msg("system", "s"), msg("user", "u"), msg("assistant", "dropped")];
    expect(commonPrefixLength(cur, prev)).toBe(2);
  });

  it("returns 0 when the first message differs (e.g. subagent system)", () => {
    const a = [msg("system", "main"), msg("user", "u")];
    const b = [msg("system", "subagent"), msg("user", "u")];
    expect(commonPrefixLength(a, b)).toBe(0);
  });

  it("returns 0 when either side is empty", () => {
    const a = [msg("user", "u")];
    expect(commonPrefixLength(a, [])).toBe(0);
    expect(commonPrefixLength([], a)).toBe(0);
    expect(commonPrefixLength([], [])).toBe(0);
  });

  it("distinguishes messages by tool_call_id", () => {
    const a = [{ role: "tool", content: "r", tool_call_id: "call_1" }];
    const b = [{ role: "tool", content: "r", tool_call_id: "call_2" }];
    expect(commonPrefixLength(a, b)).toBe(0);
  });
});
