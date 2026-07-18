import { describe, expect, it } from "vitest";
import {
  buildTree,
  collapsedPreview,
  parseJsonValue,
  primitiveDisplay,
} from "./json-tree";

describe("buildTree", () => {
  it("builds a primitive leaf", () => {
    const n = buildTree("hello");
    expect(n.type).toBe("string");
    expect(n.primitiveValue).toBe("hello");
    expect(n.size).toBe(0);
    expect(n.children).toBeUndefined();
  });

  it("builds an object node with child paths", () => {
    const n = buildTree({ a: 1, b: { c: "x" } });
    expect(n.type).toBe("object");
    expect(n.size).toBe(2);
    expect(n.children).toHaveLength(2);
    const [a, b] = n.children!;
    expect(a.path).toBe("root.a");
    expect(a.type).toBe("number");
    expect(b.path).toBe("root.b");
    expect(b.type).toBe("object");
    expect(b.children![0].path).toBe("root.b.c");
  });

  it("builds an array node with [i] paths", () => {
    const n = buildTree([10, 20, { x: true }]);
    expect(n.type).toBe("array");
    expect(n.size).toBe(3);
    expect(n.children![0].path).toBe("root[0]");
    expect(n.children![2].path).toBe("root[2]");
    expect(n.children![2].children![0].path).toBe("root[2].x");
  });

  it("treats null as a primitive", () => {
    const n = buildTree(null);
    expect(n.type).toBe("null");
    expect(n.primitiveValue).toBeNull();
  });

  it("handles nested empty object/array", () => {
    const n = buildTree({ o: {}, a: [] });
    const [o, a] = n.children!;
    expect(o.size).toBe(0);
    expect(a.size).toBe(0);
  });
});

describe("parseJsonValue", () => {
  it("parses a JSON string", () => {
    const r = parseJsonValue('{"a":1}');
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.root.type).toBe("object");
  });

  it("falls back on invalid JSON string", () => {
    const r = parseJsonValue("not json {");
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.raw).toBe("not json {");
  });

  it("passes through already-parsed objects", () => {
    const r = parseJsonValue({ x: [1, 2] });
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.root.children![0].type).toBe("array");
  });

  it("handles SSE / multi-line strings as raw fallback", () => {
    const sse = "data: {...}\n\ndata: [DONE]";
    const r = parseJsonValue(sse);
    expect(r.ok).toBe(false);
  });

  it("returns raw for undefined", () => {
    const r = parseJsonValue(undefined);
    expect(r.ok).toBe(false);
  });
});

describe("collapsedPreview", () => {
  it("previews an object with key count", () => {
    const n = buildTree({ a: 1, b: 2, c: 3 });
    expect(collapsedPreview(n)).toBe("{…} 3 keys");
  });

  it("previews a single-key object with singular form", () => {
    const n = buildTree({ only: 1 });
    expect(collapsedPreview(n)).toBe("{…} 1 key");
  });

  it("previews an array with item count", () => {
    const n = buildTree([1, 2]);
    expect(collapsedPreview(n)).toBe("[…] 2 items");
  });

  it("returns empty string for primitives", () => {
    expect(collapsedPreview(buildTree("s"))).toBe("");
  });
});

describe("primitiveDisplay", () => {
  it("quotes strings", () => {
    expect(primitiveDisplay(buildTree("hi"))).toBe('"hi"');
  });

  it("renders null as literal", () => {
    expect(primitiveDisplay(buildTree(null))).toBe("null");
  });

  it("renders numbers/booleans via String()", () => {
    expect(primitiveDisplay(buildTree(42))).toBe("42");
    expect(primitiveDisplay(buildTree(true))).toBe("true");
  });

  it("escapes inner quotes in strings", () => {
    expect(primitiveDisplay(buildTree('say "hi"'))).toBe('"say \\"hi\\""');
  });
});
