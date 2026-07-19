import { describe, expect, it } from "vitest";
import { pickBaseline } from "./trace-baseline";
import type { TraceDetailLike } from "./trace-categories";

function msg(role: string, content: string, toolCallId?: string): Record<string, unknown> {
  const m: Record<string, unknown> = { role, content };
  if (toolCallId !== undefined) m.tool_call_id = toolCallId;
  return m;
}

const SYSTEM_MAIN = msg("system", "you are the main agent");
const SYSTEM_SUB = msg("system", "you are a subagent for exploration");

describe("pickBaseline", () => {
  it("selects the closest normally-growing ancestor", () => {
    // Main branch grows: turn1 has 3 messages, turn2 reuses all 3 and adds one.
    const turn1: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "hi"), msg("assistant", "hello")],
    };
    const turn2: TraceDetailLike = {
      messages: [
        SYSTEM_MAIN,
        msg("user", "hi"),
        msg("assistant", "hello"),
        msg("user", "do X"),
      ],
    };
    // candidates ordered closest-first
    expect(pickBaseline(turn2, [turn1])).toBe(turn1);
  });

  it("skips interleaved subagent rows and picks the same-branch ancestor", () => {
    // Main agent turn1, then a subagent request interleaved, then main turn2.
    const mainTurn1: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "hi"), msg("assistant", "hello")],
    };
    const subagentReq: TraceDetailLike = {
      messages: [SYSTEM_SUB, msg("user", "explore the repo")],
    };
    const mainTurn2: TraceDetailLike = {
      messages: [
        SYSTEM_MAIN,
        msg("user", "hi"),
        msg("assistant", "hello"),
        msg("user", "now do Y"),
      ],
    };
    // Closest-first: [subagentReq, mainTurn1]. The subagent shares no prefix
    // (different system) so it must be skipped in favour of mainTurn1.
    const picked = pickBaseline(mainTurn2, [subagentReq, mainTurn1]);
    expect(picked).toBe(mainTurn1);
  });

  it("returns null when every candidate shares no prefix (fresh branch)", () => {
    const current: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "start over")],
    };
    const unrelated1: TraceDetailLike = {
      messages: [SYSTEM_SUB, msg("user", "explore")],
    };
    const unrelated2: TraceDetailLike = {
      messages: [msg("system", "totally different"), msg("user", "other")],
    };
    expect(pickBaseline(current, [unrelated1, unrelated2])).toBeNull();
  });

  it("returns null when current has no messages", () => {
    const empty: TraceDetailLike = { messages: [] };
    const cand: TraceDetailLike = { messages: [msg("user", "x")] };
    expect(pickBaseline(empty, [cand])).toBeNull();
  });

  it("breaks ties toward the closest candidate", () => {
    // Two candidates share the same prefix length with current; the first
    // (closest) should win because we only replace on strictly greater.
    const shared: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "shared"), msg("assistant", "ok")],
    };
    const candA: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "shared"), msg("assistant", "ok")],
    };
    const candB: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "shared"), msg("assistant", "ok")],
    };
    // current extends the shared prefix; both candidates offer the same length.
    const current: TraceDetailLike = {
      messages: [
        SYSTEM_MAIN,
        msg("user", "shared"),
        msg("assistant", "ok"),
        msg("user", "more"),
      ],
    };
    expect(pickBaseline(current, [candA, candB])).toBe(candA);
    expect(pickBaseline(current, [shared, candA, candB])).toBe(shared);
  });

  it("prefers a longer-prefix ancestor over a closer shorter one", () => {
    // Closest candidate shares 1 message; an older one shares 3. The older,
    // longer-prefix one is the true ancestor and should win.
    const close: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "diverged-here")],
    };
    const older: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "hi"), msg("assistant", "hello")],
    };
    const current: TraceDetailLike = {
      messages: [
        SYSTEM_MAIN,
        msg("user", "hi"),
        msg("assistant", "hello"),
        msg("user", "continue"),
      ],
    };
    expect(pickBaseline(current, [close, older])).toBe(older);
  });

  it("tolerates null/missing messages on candidates", () => {
    const current: TraceDetailLike = {
      messages: [SYSTEM_MAIN, msg("user", "hi")],
    };
    const broken: TraceDetailLike = { messages: null };
    const good: TraceDetailLike = { messages: [SYSTEM_MAIN, msg("user", "hi")] };
    expect(pickBaseline(current, [broken, good])).toBe(good);
  });
});
