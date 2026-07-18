import { useMemo, useState } from "react";
import { cn } from "../../lib/cn";
import {
  buildTree,
  collapsedPreview,
  parseJsonValue,
  primitiveDisplay,
  type JsonNode,
} from "../../lib/json-tree";

// JsonTree — collapsible JSON viewer with type coloring and copy-path.
//
// Accepts either an already-parsed value (object/array/primitive) or a raw
// string. Raw strings are JSON.parse'd; failures fall back to a <pre> block
// so SSE / non-JSON payloads still render.

export function JsonTree({
  value,
  defaultCollapsedDepth = 3,
  className,
}: {
  value: unknown;
  /** Nodes deeper than this depth start collapsed. 0 = expand root only. */
  defaultCollapsedDepth?: number;
  className?: string;
}) {
  const parsed = useMemo(() => parseJsonValue(value), [value]);

  if (!parsed.ok) {
    return (
      <pre className={cn("max-h-96 overflow-auto rounded-md border border-border bg-muted/30 p-3 text-xs", className)}>
        {parsed.raw}
      </pre>
    );
  }

  return (
    <div
      className={cn(
        "max-h-[32rem] overflow-auto rounded-md border border-border bg-muted/30 p-3 font-mono text-xs",
        className,
      )}
    >
      <JsonNodeView node={parsed.root} depth={0} defaultCollapsedDepth={defaultCollapsedDepth} />
    </div>
  );
}

function JsonNodeView({
  node,
  depth,
  defaultCollapsedDepth,
}: {
  node: JsonNode;
  depth: number;
  defaultCollapsedDepth: number;
}) {
  const [collapsed, setCollapsed] = useState(depth >= defaultCollapsedDepth && node.size > 0);
  const isContainer = node.type === "object" || node.type === "array";

  if (!isContainer) {
    return (
      <div className="leading-5">
        <KeyLabel node={node} />
        <PrimitiveValue node={node} />
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center gap-1 leading-5">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="inline-flex h-4 w-4 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
          aria-label={collapsed ? "展开" : "折叠"}
          aria-expanded={!collapsed}
        >
          <svg
            viewBox="0 0 16 16"
            className={cn("h-3 w-3 transition-transform", collapsed && "-rotate-90")}
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
          >
            <path d="M4 6l4 4 4-4" />
          </svg>
        </button>
        <KeyLabel node={node} />
        <span className="text-muted-foreground">
          {node.type === "object" ? "{" : "["}
          {collapsed && <span className="ml-1">{collapsedPreview(node)}</span>}
        </span>
        {collapsed && <span className="text-muted-foreground">{node.type === "object" ? "}" : "]"}</span>}
      </div>
      {!collapsed && (
        <>
          <div className="ml-4 border-l border-border pl-2">
            {node.children!.map((child) => (
              <JsonNodeView
                key={child.path}
                node={child}
                depth={depth + 1}
                defaultCollapsedDepth={defaultCollapsedDepth}
              />
            ))}
          </div>
          <div className="leading-5 text-muted-foreground">{node.type === "object" ? "}" : "]"}</div>
        </>
      )}
    </div>
  );
}

function KeyLabel({ node }: { node: JsonNode }) {
  // Hide the synthetic "root" key.
  if (node.path === "root") return null;
  const isIndex = /\[\d+\]$/.test(node.path);
  return (
    <span className={cn("mr-1", isIndex ? "text-muted-foreground" : "text-sky-600 dark:text-sky-400")}>
      {isIndex ? `[${node.key}]` : node.key}:
    </span>
  );
}

function PrimitiveValue({ node }: { node: JsonNode }) {
  const cls =
    node.type === "string"
      ? "text-emerald-600 dark:text-emerald-400"
      : node.type === "number"
        ? "text-amber-600 dark:text-amber-400"
        : node.type === "boolean"
          ? "text-violet-600 dark:text-violet-400"
          : "text-muted-foreground";
  return <span className={cls}>{primitiveDisplay(node)}</span>;
}

/** Re-export for callers that need the tree directly. */
export { buildTree };
