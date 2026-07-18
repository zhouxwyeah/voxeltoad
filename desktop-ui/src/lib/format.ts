// Display formatting + label maps for the desktop UI (zh-CN).

const AGENT_LABELS: Record<string, string> = {
  "claude-code": "Claude Code",
  codex: "Codex",
  codebuddy: "CodeBuddy",
  workbuddy: "WorkBuddy",
  opencode: "OpenCode",
};

export function agentLabel(agentType: string): string {
  if (!agentType) return "其他";
  return AGENT_LABELS[agentType] ?? agentType;
}

export function agentTone(agentType: string): "primary" | "success" | "warning" | "muted" | "default" {
  switch (agentType) {
    case "claude-code":
      return "primary";
    case "codebuddy":
      return "success";
    case "codex":
      return "warning";
    default:
      return "muted";
  }
}

export function formatNumber(n: number | undefined): string {
  return (n ?? 0).toLocaleString("zh-CN");
}

export function formatDuration(ms: number | undefined): string {
  const v = ms ?? 0;
  if (v < 1000) return `${v}ms`;
  return `${(v / 1000).toFixed(2)}s`;
}

export function formatTime(iso: string | undefined): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", { hour12: false });
}

export function shortId(id: string | undefined, n = 10): string {
  if (!id) return "-";
  return id.length > n ? `${id.slice(0, n)}…` : id;
}

export function statusTone(status: number | undefined): "success" | "destructive" | "warning" | "muted" {
  const s = status ?? 0;
  if (s >= 200 && s < 300) return "success";
  if (s >= 400 && s < 500) return "warning";
  if (s >= 500) return "destructive";
  return "muted";
}
