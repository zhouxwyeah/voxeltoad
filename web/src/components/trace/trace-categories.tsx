"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";

/**
 * TraceCategories renders a request's trace data as an indented summary tree
 * (IDE-style): every node defaults to a single-line chip (type + line count +
 * tool name) and expands to show the raw content only when clicked. The tree
 * has up to 5 levels, each marked by a left guide line + padding:
 *
 *   section   (system / tools / messages / output)
 *     group     (carried-over / new — only under messages)
 *       role     (user / assistant / system / tool)
 *         msg     (one message: index + block chips)
 *           block   (text / img / tool_use / tool_result / thinking)
 *
 * Block chips are colored by type (text=blue, img=orange, thinking=purple,
 * tool_use=cyan, tool_result=green, unknown=gray). Tool names get distinct
 * colored labels (read_file/write_file/bash…). Roles get a colored left bar.
 *
 * Data is always OpenAI-format (gateway only accepts OpenAI inbound; Claude
 * responses are re-encoded to OpenAI shape — ADR-0032).
 */
export function TraceCategories({
  current,
  previous,
  t,
}: {
  current: TraceDetailLike;
  previous: TraceDetailLike | null;
  t: ReturnType<typeof useTranslations>;
}) {
  const { systemMsgs, tools, carried, newUser, newAssistant, output } = analyze(
    current,
    previous,
  );
  const newMsgs = [...newUser, ...newAssistant];
  const totalMsgs = carried.length + newMsgs.length;

  if (systemMsgs.length === 0 && tools.length === 0 && totalMsgs === 0 && !output) {
    return <p className="text-sm text-muted-foreground">{t("messages.empty")}</p>;
  }

  return (
    <div className="flex flex-col gap-1 font-mono text-sm">
      {systemMsgs.length > 0 && (
        <Section label="system" count={systemMsgs.length} t={t}>
          {systemMsgs.map((m, i) => (
            <MessageNode key={i} index={i} msg={m} t={t} />
          ))}
        </Section>
      )}

      {tools.length > 0 && (
        <Section label="tools" count={tools.length} t={t}>
          <div className="flex flex-col gap-0.5 pl-4">
            {tools.map((raw, i) => (
              <ToolLabel key={i} tool={raw} t={t} />
            ))}
          </div>
        </Section>
      )}

      {totalMsgs > 0 && (
        <Section label="messages" count={totalMsgs} t={t}>
          {carried.length > 0 && (
            <Group label="carried-over" count={carried.length} t={t}>
              {groupMessagesByRole(carried).map((g) => (
                <RoleGroup key={g.role} role={g.role} msgs={g.msgs} t={t} />
              ))}
            </Group>
          )}
          {newMsgs.length > 0 && (
            <Group label="new" count={newMsgs.length} t={t}>
              {groupMessagesByRole(newMsgs).map((g) => (
                <RoleGroup key={g.role} role={g.role} msgs={g.msgs} t={t} />
              ))}
            </Group>
          )}
        </Section>
      )}

      <Section
        label="output"
        count={output ? 1 : 0}
        t={t}
        emptyHint={t("raw.empty")}
      >
        {output && <OutputNode item={output} t={t} />}
      </Section>
    </div>
  );
}

// --- types ---

type TraceDetailLike = {
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

// --- analysis (pure) ---

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
  const carried = curMsgs.slice(0, prefixLen);
  const turnMsgs = curMsgs.slice(prefixLen);

  const systemMsgs: Record<string, unknown>[] = [];
  const newUser: Record<string, unknown>[] = [];
  const newAssistant: Record<string, unknown>[] = [];
  for (const m of curMsgs) {
    if (String(m.role ?? "") === "system") systemMsgs.push(m);
  }
  for (const m of turnMsgs) {
    const role = String(m.role ?? "");
    if (role === "system") continue;
    if (role === "user") newUser.push(m);
    else newAssistant.push(m);
  }

  const tools = extractTools(current.request_raw);
  const output = parseOutput(current.response_raw);

  return { systemMsgs, tools, carried, newUser, newAssistant, output };
}

function groupMessagesByRole(
  msgs: Record<string, unknown>[],
): { role: string; msgs: Record<string, unknown>[] }[] {
  const order = ["user", "assistant", "system", "tool"];
  const buckets = new Map<string, Record<string, unknown>[]>();
  for (const m of msgs) {
    const role = String(m.role ?? "assistant");
    if (!buckets.has(role)) buckets.set(role, []);
    buckets.get(role)!.push(m);
  }
  const result: { role: string; msgs: Record<string, unknown>[] }[] = [];
  for (const role of order) {
    const g = buckets.get(role);
    if (g && g.length > 0) result.push({ role, msgs: g });
  }
  for (const [role, g] of buckets) {
    if (!order.includes(role)) result.push({ role, msgs: g });
  }
  return result;
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

  // response_raw is TEXT in the database — the API returns it as a JSON
  // string, not a parsed object. Try to parse it first.
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

  // Non-streaming: parsed JSON with choices[0].message.
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

  // Not valid chat completion JSON — try SSE reassembly.
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
  t,
  emptyHint,
  children,
}: {
  label: string;
  count: number;
  t: ReturnType<typeof useTranslations>;
  emptyHint?: string;
  children?: React.ReactNode;
}) {
  const [open, setOpen] = useState(true);
  const title = labelTitle(label, t);
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
  t,
  children,
}: {
  label: string;
  count: number;
  t: ReturnType<typeof useTranslations>;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(label === "new");
  const title =
    label === "carried-over"
      ? t("messages.categories.carriedOver")
      : t("messages.categories.new");
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

function RoleGroup({
  role,
  msgs,
  t,
}: {
  role: string;
  msgs: Record<string, unknown>[];
  t: ReturnType<typeof useTranslations>;
}) {
  const [open, setOpen] = useState(true);
  return (
    <div className={`ml-2 border-l pl-2 ${roleBarColor(role)}`}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 text-left text-xs font-semibold uppercase tracking-wide"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        <span className={roleTextColor(role)}>{role}</span>
        <span className="text-muted-foreground/70">· {msgs.length}</span>
      </button>
      {open && (
        <div className="mt-0.5 flex flex-col gap-0.5">
          {msgs.map((m, i) => (
            <MessageNode key={i} index={i} msg={m} t={t} />
          ))}
        </div>
      )}
    </div>
  );
}

function MessageNode({
  index,
  msg,
  t,
}: {
  index: number;
  msg: Record<string, unknown>;
  t: ReturnType<typeof useTranslations>;
}) {
  const [open, setOpen] = useState(false);
  const blocks = blocksOf(msg);

  return (
    <div className="ml-2 border-l border-border/30 pl-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 py-0.5 text-left text-xs hover:bg-accent/30"
      >
        <span className="text-muted-foreground">{open ? "▾" : "▸"}</span>
        <span className="text-muted-foreground/70">#{index + 1}</span>
        <span className="flex flex-wrap items-center gap-1">
          {blocks.map((b, i) => (
            <BlockChip key={i} block={b} />
          ))}
        </span>
      </button>
      {open && (
        <div className="mt-0.5 flex flex-col gap-1 pb-1">
          {blocks.map((b, i) => (
            <BlockRaw key={i} block={b} t={t} />
          ))}
        </div>
      )}
    </div>
  );
}

function OutputNode({
  item,
  t,
}: {
  item: OutputItem;
  t: ReturnType<typeof useTranslations>;
}) {
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
          item.toolCalls.map((tc, i) => (
            <ToolCallChip key={i} tc={tc as Record<string, unknown>} />
          ))}
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
              <ToolCallRaw key={i} tc={tc as Record<string, unknown>} t={t} />
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

// blocksOf turns a message into the list of display blocks. content (string |
// array) yields text/image/thinking/unknown blocks; tool_calls yield tool_use
// blocks; a tool-role message with tool_call_id yields a tool_result.
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

// ToolLabel renders one tool definition as a single row: a colored name chip on
// the left + a one-line truncated description on the right. Clicking the row
// expands the full description and the parameters schema. Laid out vertically
// (one tool per line) so a tool list reads like a directory listing.
function ToolLabel({
  tool,
  t,
}: {
  tool: Record<string, unknown>;
  t: ReturnType<typeof useTranslations>;
}) {
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
        <span className="truncate text-muted-foreground">
          {desc ? desc : t("raw.empty")}
        </span>
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

function BlockRaw({
  block,
  t,
}: {
  block: Block;
  t: ReturnType<typeof useTranslations>;
}) {
  switch (block.kind) {
    case "text":
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
      try { argsPretty = JSON.stringify(JSON.parse(argsRaw), null, 2); } catch { /* raw */ }
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

function ToolCallRaw({
  tc,
  t,
}: {
  tc: Record<string, unknown>;
  t: ReturnType<typeof useTranslations>;
}) {
  const fn = (tc.function ?? {}) as Record<string, unknown>;
  const name = String(fn.name ?? "(tool)");
  const argsRaw =
    typeof fn.arguments === "string"
      ? fn.arguments
      : JSON.stringify(fn.arguments ?? {});
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

function labelTitle(label: string, t: ReturnType<typeof useTranslations>): string {
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

function roleBarColor(role: string): string {
  switch (role) {
    case "user":
      return "border-blue-300 dark:border-blue-800";
    case "assistant":
      return "border-emerald-300 dark:border-emerald-800";
    case "system":
      return "border-amber-300 dark:border-amber-800";
    case "tool":
      return "border-purple-300 dark:border-purple-800";
    default:
      return "border-border";
  }
}

function roleTextColor(role: string): string {
  switch (role) {
    case "user":
      return "text-blue-600 dark:text-blue-400";
    case "assistant":
      return "text-emerald-600 dark:text-emerald-400";
    case "system":
      return "text-amber-600 dark:text-amber-400";
    case "tool":
      return "text-purple-600 dark:text-purple-400";
    default:
      return "text-muted-foreground";
  }
}

// toolLabelColor: a stable per-tool-name color so read_file/write_file/bash
// are visually distinct. Hash the name to one of a small palette.
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
