"use client";

import { useTranslations } from "next-intl";
import { CircleAlert } from "lucide-react";
import { microToDisplay } from "@/lib/money";
import type { SessionSummary } from "./page";

/**
 * Trace sessions client: renders the server-aggregated session list (per-session
 * token, duration, cost, and agent totals from /api/v1/request-logs/sessions)
 * with an agent-type filter. Each session links to its trace detail page.
 */
export function TraceSessionsClient({
  sessions,
  anyRows,
  agentFilter,
}: {
  sessions: SessionSummary[];
  anyRows: boolean;
  agentFilter: string;
}) {
  const t = useTranslations("trace");

  const agentOptions = [
    "claude-code",
    "codex",
    "codebuddy",
    "workbuddy",
    "opencode",
  ];

  return (
    <>
      <div className="flex flex-col gap-2">
        <h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1>
        <p className="text-sm text-muted-foreground">{t("subtitle")}</p>
      </div>

      {/* Agent-type filter (navigates with a query param so the RSC re-fetches). */}
      <form className="flex items-center gap-2 text-sm">
        <label
          htmlFor="agent-filter"
          className="text-muted-foreground"
        >
          {t("agent.type")}
        </label>
        <select
          id="agent-filter"
          name="agent_type"
          defaultValue={agentFilter}
          onChange={(e) => {
            const v = e.target.value;
            const url = new URL(window.location.href);
            if (v) url.searchParams.set("agent_type", v);
            else url.searchParams.delete("agent_type");
            window.location.assign(url.toString());
          }}
          className="rounded-md border border-border bg-background px-2 py-1 text-sm"
        >
          <option value="">{t("agent.all")}</option>
          {agentOptions.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
      </form>

      {sessions.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {anyRows ? t("notFound") : t("captureDisabled")}
        </p>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted text-left">
                <Th>{t("sessions.session")}</Th>
                <Th>{t("agent.type")}</Th>
                <Th className="text-right">{t("sessions.requests")}</Th>
                <Th className="text-right">{t("sessions.tokenTotal")}</Th>
                <Th className="text-right">{t("sessions.duration")}</Th>
                <Th className="text-right">{t("sessions.cost")}</Th>
                <Th>{t("sessions.startedAt")}</Th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr
                  key={s.session_id}
                  className="border-b border-border last:border-b-0 hover:bg-accent/40"
                >
                  <td className="px-3 py-2">
                    <a
                      href={`/trace/sessions/${encodeURIComponent(s.session_id)}`}
                      className="font-mono text-primary underline-offset-2 hover:underline"
                    >
                      {s.session_id}
                    </a>
                    {s.has_errors && (
                      <CircleAlert className="ml-2 inline h-3.5 w-3.5 text-destructive" />
                    )}
                  </td>
                  <td className="px-3 py-2">{s.agent_type || "—"}</td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {s.request_count}
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                    <span className="text-foreground">
                      {formatTokens(s.total_tokens)}
                    </span>
                    <span className="ml-1 text-xs">
                      ({formatTokens(s.prompt_tokens)}/
                      {formatTokens(s.completion_tokens)})
                    </span>
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {formatDuration(s.duration_ms)}
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {s.cost > 0 ? microToDisplay(s.cost) : "—"}
                  </td>
                  <td className="px-3 py-2">
                    {s.started_at
                      ? new Date(s.started_at).toLocaleString()
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// formatTokens renders an integer token count with a k/M suffix for readability
// in dense table cells (e.g. 11300 → "11.3k").
function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

// formatDuration renders a millisecond total as a compact human string
// (e.g. 17586 → "17.6s", 800 → "0.8s", 120000 → "2.0m").
function formatDuration(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.floor((ms % 60_000) / 1000);
  return `${m}m${s}s`;
}

function Th({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <th
      className={`px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground ${className}`}
    >
      {children}
    </th>
  );
}
