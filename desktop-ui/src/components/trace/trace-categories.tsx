import * as React from "react";
import { useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

/**
 * TraceCategories — ported from the admin UI (web/src/components/trace/
 * trace-categories.tsx). Renders a request's trace data as an indented
 * IDE-style summary tree: every node defaults to a single-line chip and
 * expands to show raw content on click. The user's "学提示词" value lives here
 * — it makes each request's messages, tool calls, and output scannable.
 *
 * Differences from the admin original: the next-intl `useTranslations` hook is
 * replaced by a local Chinese label map (the desktop UI is single-locale
 * zh-CN), and only the data shape matters (messages / request_raw / response_raw
 * are already JSON-decoded by the fetch layer).
 */

const LABELS: Record<string, string> = {
  "messages.empty": "无消息内容",
  "messages.categories.system": "系统",
  "messages.categories.tools": "工具",
  "messages.categories.messages": "消息",
  "messages.categories.output": "输出",
  "messages.categories.carriedOver": "沿用",
  "messages.categories.new": "新增",
  "messages.categories.toolsParameters": "参数",
  "raw.empty": "（空）",
};

function t(key: string): string {
  return LABELS[key] ?? key;
}

export type TraceDetailLike = {
  messages?: unknown;
  request_raw?: unknown;
  response_raw?: unknown;
  stream?: boolean;
};

type OutputItem = {
  text: string;
  toolCalls?: unknown[];
  raw: string;
};

function analyze(current: TraceDetailLike, previous: TraceDetailLike | null) {
  const curMsgs = (Array.isArray(current.messages) ? current.messages : []) as Record<
    string,
    unknown
  >[];
  const prevMsgs = (Array.isArray(previous?.messages) ? previous!.messages : []) as Record<
    string,
    unknown
  >[];

  const prefixLen = commonPrefixLength(curMsgs, prevMsgs);
  // system messages get their own top-level section, so exclude them from
  // the chronological messages list (they'd otherwise appear twice).
  const notSystem = (m: Record<string, unknown>) => String(m.role ?? "") !== "system";
  const carried = curMsgs.slice(0, prefixLen).filter(notSystem);
  const turnMsgs = curMsgs.slice(prefixLen).filter(notSystem);

  const systemMsgs: Record<string, unknown>[] = [];
  for (const m of curMsgs) {
    if (String(m.role ?? "") === "system") systemMsgs.push(m);
  }

  const tools = extractTools(current.request_raw);
  const output = parseOutput(current.response_raw);

  return { systemMsgs, tools, carried, turnMsgs, output };
}

function signature(m: Record<string, unknown>): string {
  return `${m.role ?? ""}|${JSON.stringify(m.content ?? "")}|${m.tool_call_id ?? ""}`;
}

function commonPrefixLength(
  a: Record<string, unknown>[],
  b: Record<string, unknown>[],
): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (signature(a[i]) !== signature(b[i])) return i;
  }
  return n;
}

function extractTools(requestRaw: unknown): Record<string, unknown>[] {
  if (typeof requestRaw !== "object" || requestRaw === null) return [];
  const tools = (requestRaw as Record<string, unknown>).tools;
  if (!Array.isArray(tools)) return [];
  return tools as Record<string, unknown>[];
}

function parseOutput(responseRaw: unknown): OutputItem | null {
  if (responseRaw == null || responseRaw === "") return null;

  let obj: Record<string, unknown> | null = null;
  if (typeof responseRaw === "object" && responseRaw !== null) {
    obj = responseRaw as Record<string, unknown>;
  } else if (typeof responseRaw === "string") {
    try {
      obj = JSON.parse(responseRaw);
    } catch {
      // Not JSON — probably an SSE transcript. Fall through to the SSE branch.
    }
  }

  const raw =
    typeof responseRaw === "string" ? responseRaw : JSON.stringify(responseRaw);

  if (obj && Array.isArray(obj.choices) && obj.choices.length > 0) {
    const msg = (obj.choices[0] as Record<string, unknown>)?.message as
      | Record<string, unknown>
      | undefined;
    if (msg) {
      return {
        text: flattenContent(msg.content),
        toolCalls: Array.isArray(msg.tool_calls) ? msg.tool_calls : undefined,
        raw,
      };
    }
  }

  if (typeof responseRaw === "string") {
    try {
      const r = reassembleSSE(responseRaw);
      if (r) {
        return {
          text: r.content,
          toolCalls: r.toolCalls.length > 0 ? r.toolCalls : undefined,
          raw,
        };
      }
    } catch {
      // fall through
    }
  }

  return { text: raw, raw };
}

function reassembleSSE(transcript: string): {
  content: string;
  toolCalls: unknown[];
} | null {
  const lines = transcript.split("\n");
  let content = "";
  const toolCallsByIndex = new Map<number, Record<string, unknown>>();
  let sawAny = false;
  for (const line of lines) {
    const trimmed = line.trimStart();
    if (!trimmed.startsWith("data:")) continue;
    const payload = trimmed.slice(5).trim();
    if (payload === "[DONE]" || payload === "") continue;
    let chunk: Record<string, unknown>;
    try {
      chunk = JSON.parse(payload);
    } catch {
      continue;
    }
    sawAny = true;
    const choices = Array.isArray(chunk.choices) ? chunk.choices : [];
    const delta = (choices[0] as Record<string, unknown> | undefined)?.delta as
      | Record<string, unknown>
      | undefined;
    if (!delta) continue;
    if (typeof delta.content === "string") content += delta.content;
    if (Array.isArray(delta.tool_calls)) {
      for (const tc of delta.tool_calls) {
        const tcr = tc as Record<string, unknown>;
        const idx = typeof tcr.index === "number" ? tcr.index : 0;
        const existing =
          toolCallsByIndex.get(idx) ?? {
            id: "",
            type: "function",
            function: { name: "", arguments: "" },
          };
        const fn = (existing.function ?? {}) as Record<string, unknown>;
        const newFn = (tcr.function ?? {}) as Record<string, unknown>;
        if (typeof newFn.name === "string")
          fn.name = (fn.name as string) + newFn.name;
        if (typeof newFn.arguments === "string")
          fn.arguments = (fn.arguments as string) + newFn.arguments;
        if (typeof tcr.id === "string" && tcr.id) existing.id = tcr.id;
        existing.function = fn;
        toolCallsByIndex.set(idx, existing);
      }
    }
  }
  if (!sawAny) return null;
  return {
    content,
    toolCalls: Array.from(toolCallsByIndex.entries())
      .sort((a, b) => a[0] - b[0])
      .map(([, v]) => v),
  };
}

// --- tree nodes ---

function Section({
  label,
  count,
  emptyHint,
  children,
}: {
  label: string;
  count: number;
  emptyHint?: string;
  children?: React.ReactNode;
}) {
  const [open, setOpen] = useState(true);
  const title = labelTitle(label);
  return (
    <div className={`rounded-md border ${sectionColor(label)}`}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2 py-1 text-left text-xs font-semibold uppercase tracking-wide"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        <span className="text-foreground">{title}</span>
        <span className="text-muted-foreground">· {count}</span>
      </button>
      {open && (
        <div className="flex flex-col gap-1.5 border-t border-border/40 px-2 py-1.5">
          {count === 0 && emptyHint ? (
            <span className="pl-4 text-xs text-muted-foreground">{emptyHint}</span>
          ) : (
            children
          )}
        </div>
      )}
    </div>
  );
}

function Group({
  label,
  count,
  children,
}: {
  label: string;
  count: number;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(label === "new");
  const title = label === "carried-over" ? t("messages.categories.carriedOver") : t("messages.categories.new");
  return (
    <div className="ml-2 border-l border-border/40 pl-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 text-left text-xs font-semibold"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        <span className="text-muted-foreground">{title}</span>
        <span className="text-muted-foreground/70">· {count}</span>
      </button>
      {open && <div className="mt-0.5 flex flex-col gap-1">{children}</div>}
    </div>
  );
}

function MessageNode({ index, msg }: { index: number; msg: Record<string, unknown> }) {
  const [open, setOpen] = useState(false);
  const blocks = blocksOf(msg);
  const role = String(msg.role ?? "");

  return (
    <div className="ml-2 border-l border-border/30 pl-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 py-0.5 text-left text-xs hover:bg-accent/30"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        <span className="text-muted-foreground/70">#{index + 1}</span>
        <span className={`rounded px-1 py-0 text-[10px] font-medium ${roleBadgeColor(role)}`}>
          {role || "unknown"}
        </span>
        <span className="flex flex-wrap items-center gap-1">
          {blocks.map((b, i) => (
            <BlockChip key={i} block={b} />
          ))}
        </span>
      </button>
      {open && (
        <div className="mt-0.5 flex flex-col gap-1 pb-1">
          {blocks.map((b, i) => (
            <BlockRaw key={i} block={b} role={role} />
          ))}
        </div>
      )}
    </div>
  );
}

function roleBadgeColor(role: string): string {
  switch (role) {
    case "user":
      return "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
    case "assistant":
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    case "system":
      return "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "tool":
      return "bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300";
    default:
      return "bg-muted text-muted-foreground";
  }
}

function OutputNode({ item }: { item: OutputItem }) {
  const [open, setOpen] = useState(false);
  return (
    <div className={`ml-2 border-l pl-2 ${indigoBar}`}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 py-0.5 text-left text-xs hover:bg-accent/30"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        {item.text ? (
          <BlockChip block={{ kind: "text", text: item.text }} />
        ) : (
          <span className="text-muted-foreground">{t("raw.empty")}</span>
        )}
        {item.toolCalls &&
          item.toolCalls.map((tc, i) => <ToolCallChip key={i} tc={tc as Record<string, unknown>} />)}
      </button>
      {open && (
        <div className="mt-0.5 flex flex-col gap-1 pb-1">
          {item.text ? (
            <pre className="whitespace-pre-wrap break-words rounded bg-muted/40 p-1.5 text-xs text-foreground">
              {item.text}
            </pre>
          ) : null}
          {item.toolCalls &&
            item.toolCalls.map((tc, i) => (
              <ToolCallRaw key={i} tc={tc as Record<string, unknown>} />
            ))}
        </div>
      )}
    </div>
  );
}

// --- block model ---

type Block =
  | { kind: "text"; text: string }
  | { kind: "image" }
  | { kind: "thinking"; text: string }
  | { kind: "tool_use"; toolName: string; raw: unknown }
  | { kind: "tool_result"; callId: string; text: string }
  | { kind: "unknown"; raw: unknown };

function blocksOf(msg: Record<string, unknown>): Block[] {
  const blocks: Block[] = [];
  const role = String(msg.role ?? "");
  const content = msg.content;

  if (typeof content === "string") {
    if (content !== "") blocks.push({ kind: "text", text: content });
  } else if (Array.isArray(content)) {
    for (const part of content) {
      const p = part as Record<string, unknown>;
      const type = String(p.type ?? "");
      if (type === "text" && typeof p.text === "string") {
        blocks.push({ kind: "text", text: p.text });
      } else if (type === "image_url") {
        blocks.push({ kind: "image" });
      } else if (type === "thinking" || type === "reasoning") {
        blocks.push({ kind: "thinking", text: String(p.thinking ?? p.text ?? "") });
      } else {
        blocks.push({ kind: "unknown", raw: part });
      }
    }
  } else if (content != null) {
    blocks.push({ kind: "unknown", raw: content });
  }

  if (Array.isArray(msg.tool_calls)) {
    for (const tc of msg.tool_calls) {
      const fn = ((tc as Record<string, unknown>).function ?? {}) as Record<string, unknown>;
      blocks.push({ kind: "tool_use", toolName: String(fn.name ?? "(tool)"), raw: tc });
    }
  }

  if (role === "tool" || (typeof msg.tool_call_id === "string" && msg.tool_call_id)) {
    blocks.unshift({
      kind: "tool_result",
      callId: String(msg.tool_call_id ?? ""),
      text: typeof content === "string" ? content : JSON.stringify(content ?? ""),
    });
  }

  return blocks;
}

function countLines(text: string): number {
  if (!text) return 0;
  return text.split("\n").length;
}

// --- chips (collapsed summary) ---

function BlockChip({ block }: { block: Block }) {
  const { label, cls } = blockChipStyle(block);
  const detail = blockChipDetail(block);
  return (
    <span
      className={`inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium ${cls}`}
    >
      {label}
      {detail ? <span className="ml-1 opacity-70">{detail}</span> : null}
    </span>
  );
}

function blockChipStyle(block: Block): { label: string; cls: string } {
  switch (block.kind) {
    case "text":
      return { label: "text", cls: "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300" };
    case "image":
      return { label: "img", cls: "bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-300" };
    case "thinking":
      return { label: "thinking", cls: "bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300" };
    case "tool_use":
      return { label: "🔧", cls: "bg-cyan-100 text-cyan-700 dark:bg-cyan-950 dark:text-cyan-300" };
    case "tool_result":
      return { label: "tool_result", cls: "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300" };
    case "unknown":
      return { label: "?", cls: "bg-muted text-muted-foreground" };
  }
}

function blockChipDetail(block: Block): string {
  switch (block.kind) {
    case "text":
      return `${countLines(block.text)}行`;
    case "thinking":
      return `${countLines(block.text)}行`;
    case "tool_use":
      return block.toolName;
    case "tool_result":
      return block.callId ? `id:${shortenId(block.callId)}` : "";
    case "image":
    case "unknown":
      return "";
  }
}

function shortenId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 6)}…` : id;
}

function ToolCallChip({ tc }: { tc: Record<string, unknown> }) {
  const fn = (tc.function ?? {}) as Record<string, unknown>;
  const name = String(fn.name ?? "(tool)");
  return (
    <span
      className={`inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium ${toolLabelColor(name)}`}
    >
      🔧 {name}
    </span>
  );
}

function ToolLabel({ tool }: { tool: Record<string, unknown> }) {
  const [open, setOpen] = useState(false);
  const fn = (tool.function ?? {}) as Record<string, unknown>;
  const name = String(fn.name ?? "(unnamed)");
  const desc = typeof fn.description === "string" ? fn.description : "";
  const params = fn.parameters;
  const hasParams = params != null && typeof params === "object";
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 py-0.5 text-left text-xs hover:bg-accent/30"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        <span
          className={`inline-flex shrink-0 items-center rounded px-1.5 py-0.5 font-medium ${toolLabelColor(name)}`}
        >
          {name}
        </span>
        <span className="truncate text-muted-foreground">{desc ? desc : t("raw.empty")}</span>
      </button>
      {open && (
        <div className="mt-0.5 flex flex-col gap-1 pb-1 pl-6">
          {desc ? (
            <div className="whitespace-pre-wrap break-words rounded bg-muted/30 p-1.5 text-xs text-foreground">
              {desc}
            </div>
          ) : (
            <div className="italic text-muted-foreground">{t("raw.empty")}</div>
          )}
          {hasParams && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground">
                {t("messages.categories.toolsParameters")}
              </summary>
              <pre className="mt-1 overflow-x-auto rounded bg-muted/60 p-2">
                {JSON.stringify(params, null, 2)}
              </pre>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

// --- raw (expanded) ---

function BlockRaw({ block, role }: { block: Block; role?: string }) {
  switch (block.kind) {
    case "text":
      // Render assistant text as Markdown for a chat-like reading experience;
      // other roles stay as plain <pre> per design/desktop.md §12.
      if (role === "assistant" && block.text) {
        return (
          <div className="rounded bg-muted/40 p-2 text-xs leading-relaxed text-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[11px] [&_h1]:mb-1 [&_h1]:text-sm [&_h1]:font-semibold [&_h2]:mb-1 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:mb-1 [&_h3]:text-xs [&_h3]:font-semibold [&_li]:ml-4 [&_li]:list-disc [&_ol_li]:list-decimal [&_p]:mb-1 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-1.5 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_table]:border-collapse [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-0.5 [&_th]:border [&_th]:border-border [&_th]:bg-muted [&_th]:px-2 [&_th]:py-0.5 [&_ul]:mb-1">
            <Markdown remarkPlugins={[remarkGfm]}>{block.text}</Markdown>
          </div>
        );
      }
      return (
        <pre className="whitespace-pre-wrap break-words rounded bg-muted/40 p-1.5 text-xs text-foreground">
          {block.text || t("raw.empty")}
        </pre>
      );
    case "thinking":
      return (
        <pre className="whitespace-pre-wrap break-words rounded bg-muted/40 p-1.5 text-xs text-foreground">
          {block.text || t("raw.empty")}
        </pre>
      );
    case "tool_result":
      return (
        <pre className="whitespace-pre-wrap break-words rounded bg-emerald-50/50 p-1.5 text-xs text-foreground dark:bg-emerald-950/20">
          {block.text || t("raw.empty")}
        </pre>
      );
    case "image":
      return <span className="text-xs italic text-muted-foreground">[image]</span>;
    case "tool_use": {
      const tc = block.raw as Record<string, unknown>;
      const fn = (tc.function ?? {}) as Record<string, unknown>;
      const name = String(fn.name ?? block.toolName ?? "(tool)");
      const argsRaw =
        typeof fn.arguments === "string"
          ? fn.arguments
          : JSON.stringify(fn.arguments ?? {});
      const id = typeof tc.id === "string" ? tc.id : "";
      let argsPretty = argsRaw;
      try {
        argsPretty = JSON.stringify(JSON.parse(argsRaw), null, 2);
      } catch {
        /* raw */
      }
      return (
        <div className="rounded bg-cyan-50/40 p-1.5 text-xs dark:bg-cyan-950/20">
          <div className="mb-0.5 flex items-center gap-1">
            <span className={`rounded px-1 py-0.5 font-medium ${toolLabelColor(name)}`}>
              🔧 {name}
            </span>
            {id && <span className="font-mono text-muted-foreground">id: {id}</span>}
          </div>
          <pre className="overflow-x-auto rounded bg-muted/60 p-1.5">{argsPretty}</pre>
        </div>
      );
    }
    case "unknown":
      return (
        <pre className="overflow-x-auto rounded bg-muted/60 p-1.5 text-xs">
          {JSON.stringify(block.raw, null, 2)}
        </pre>
      );
  }
}

function ToolCallRaw({ tc }: { tc: Record<string, unknown> }) {
  const fn = (tc.function ?? {}) as Record<string, unknown>;
  const name = String(fn.name ?? "(tool)");
  const argsRaw =
    typeof fn.arguments === "string" ? fn.arguments : JSON.stringify(fn.arguments ?? {});
  const id = typeof tc.id === "string" ? tc.id : "";
  let argsPretty = argsRaw;
  try {
    argsPretty = JSON.stringify(JSON.parse(argsRaw), null, 2);
  } catch {
    // partial JSON during streaming; keep raw
  }
  return (
    <div className="rounded bg-cyan-50/40 p-1.5 text-xs dark:bg-cyan-950/20">
      <div className="mb-0.5 flex items-center gap-1">
        <span className={`rounded px-1 py-0.5 font-medium ${toolLabelColor(name)}`}>
          🔧 {name}
        </span>
        {id && <span className="font-mono text-muted-foreground">id: {id}</span>}
      </div>
      <pre className="overflow-x-auto rounded bg-muted/60 p-1.5">{argsPretty}</pre>
    </div>
  );
}

// --- colors ---

function sectionColor(label: string): string {
  switch (label) {
    case "system":
      return "border-amber-200 bg-amber-50/50 dark:border-amber-900 dark:bg-amber-950/20";
    case "tools":
      return "border-cyan-200 bg-cyan-50/50 dark:border-cyan-900 dark:bg-cyan-950/20";
    case "messages":
      return "border-slate-200 bg-slate-50/50 dark:border-slate-700 dark:bg-slate-900/20";
    case "output":
      return "border-indigo-200 bg-indigo-50/50 dark:border-indigo-900 dark:bg-indigo-950/20";
    default:
      return "border-border bg-muted/30";
  }
}

function labelTitle(label: string): string {
  switch (label) {
    case "system":
      return t("messages.categories.system");
    case "tools":
      return t("messages.categories.tools");
    case "messages":
      return t("messages.categories.messages");
    case "output":
      return t("messages.categories.output");
    default:
      return label;
  }
}

function toolLabelColor(name: string): string {
  const palette = [
    "bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300",
    "bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300",
    "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300",
    "bg-teal-100 text-teal-700 dark:bg-teal-950 dark:text-teal-300",
    "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300",
    "bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-950 dark:text-fuchsia-300",
  ];
  let h = 0;
  for (let i = 0; i < name.length; i++) {
    h = (h * 31 + name.charCodeAt(i)) >>> 0;
  }
  return palette[h % palette.length];
}

const indigoBar = "border-indigo-300 dark:border-indigo-800";

function flattenContent(content: unknown): string {
  if (content == null) return "";
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((part) => {
        const p = part as Record<string, unknown>;
        if (p.type === "text" && typeof p.text === "string") return p.text;
        if (p.type === "image_url") return "[image]";
        return JSON.stringify(p);
      })
      .join("\n");
  }
  return JSON.stringify(content, null, 2);
}

// --- public component ---

export function TraceCategories({
  current,
  previous,
}: {
  current: TraceDetailLike;
  previous: TraceDetailLike | null;
}) {
  const { systemMsgs, tools, carried, turnMsgs, output } = analyze(current, previous);
  const totalMsgs = carried.length + turnMsgs.length;

  if (systemMsgs.length === 0 && tools.length === 0 && totalMsgs === 0 && !output) {
    return <p className="text-sm text-muted-foreground">{t("messages.empty")}</p>;
  }

  return (
    <div className="flex flex-col gap-1 font-mono text-sm">
      {systemMsgs.length > 0 && (
        <Section label="system" count={systemMsgs.length}>
          {systemMsgs.map((m, i) => (
            <MessageNode key={i} index={i} msg={m} />
          ))}
        </Section>
      )}

      {tools.length > 0 && (
        <Section label="tools" count={tools.length}>
          <div className="flex flex-col gap-0.5 pl-4">
            {tools.map((raw, i) => (
              <ToolLabel key={i} tool={raw} />
            ))}
          </div>
        </Section>
      )}

      {totalMsgs > 0 && (
        <Section label="messages" count={totalMsgs}>
          {carried.length > 0 && (
            <Group label="carried-over" count={carried.length}>
              {carried.map((m, i) => (
                <MessageNode key={i} index={i} msg={m} />
              ))}
            </Group>
          )}
          {turnMsgs.length > 0 && (
            <Group label="new" count={turnMsgs.length}>
              {turnMsgs.map((m, i) => (
                <MessageNode key={i} index={carried.length + i} msg={m} />
              ))}
            </Group>
          )}
        </Section>
      )}

      <Section label="output" count={output ? 1 : 0} emptyHint={t("raw.empty")}>
        {output && <OutputNode item={output} />}
      </Section>
    </div>
  );
}
