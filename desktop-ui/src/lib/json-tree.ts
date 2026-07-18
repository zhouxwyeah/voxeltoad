// Pure helpers backing components/trace/json-tree.tsx.
// Kept in lib/ so they can be unit-tested under the `node` vitest env
// (design/desktop.md §11 defers component rendering tests).

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonNodeType = "object" | "array" | "string" | "number" | "boolean" | "null";

export interface JsonNode {
  /** Unique path from root, e.g. "root.messages[0].content". Used for copy-path. */
  path: string;
  /** Key within parent (object key or array index as string). Root is "root". */
  key: string;
  type: JsonNodeType;
  /** Present only for primitives. */
  primitiveValue?: string | number | boolean | null;
  /** Present only for object/array. Lazy-built children. */
  children?: JsonNode[];
  /** object/array length; 0 for primitives. */
  size: number;
}

function typeOf(v: JsonValue): JsonNodeType {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  const t = typeof v;
  if (t === "string") return "string";
  if (t === "number") return "number";
  if (t === "boolean") return "boolean";
  return "object";
}

function childPath(parentPath: string, key: string, isIndex: boolean): string {
  return isIndex ? `${parentPath}[${key}]` : `${parentPath}.${key}`;
}

/** Build a JsonNode tree from an already-parsed JSON value. */
export function buildTree(value: JsonValue, key = "root", path = "root"): JsonNode {
  const type = typeOf(value);
  if (type === "object") {
    const entries = Object.entries(value as Record<string, JsonValue>);
    return {
      path,
      key,
      type,
      size: entries.length,
      children: entries.map(([k, v]) => buildTree(v, k, childPath(path, k, false))),
    };
  }
  if (type === "array") {
    const arr = value as JsonValue[];
    return {
      path,
      key,
      type,
      size: arr.length,
      children: arr.map((v, i) => buildTree(v, String(i), childPath(path, String(i), true))),
    };
  }
  return {
    path,
    key,
    type,
    primitiveValue: value as string | number | boolean | null,
    size: 0,
  };
}

export type ParseResult =
  | { ok: true; root: JsonNode }
  | { ok: false; raw: string };

/**
 * Try to parse `input` as JSON. Accepts either an already-parsed value
 * (object/array/primitive) or a raw string. On string parse failure, returns
 * `{ ok: false, raw }` so the caller can fall back to a <pre> block.
 */
export function parseJsonValue(input: unknown): ParseResult {
  if (typeof input === "string") {
    try {
      const parsed = JSON.parse(input) as JsonValue;
      return { ok: true, root: buildTree(parsed) };
    } catch {
      return { ok: false, raw: input };
    }
  }
  if (input === null || typeof input === "object" || Array.isArray(input)) {
    return { ok: true, root: buildTree(input as JsonValue) };
  }
  if (typeof input === "number" || typeof input === "boolean") {
    return { ok: true, root: buildTree(input) };
  }
  // undefined / function / symbol → render as empty string fallback
  return { ok: false, raw: String(input) };
}

/** Short preview for a collapsed object/array: "{...} 5 keys" or "[...] 3 items". */
export function collapsedPreview(node: JsonNode): string {
  if (node.type === "object") return `{…} ${node.size} ${node.size === 1 ? "key" : "keys"}`;
  if (node.type === "array") return `[…] ${node.size} ${node.size === 1 ? "item" : "items"}`;
  return "";
}

/** Format a primitive value for display. Strings are quoted. */
export function primitiveDisplay(node: JsonNode): string {
  if (node.type === "string") return JSON.stringify(node.primitiveValue);
  if (node.type === "null") return "null";
  return String(node.primitiveValue);
}
