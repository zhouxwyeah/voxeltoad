"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui";
import { TraceCategories } from "@/components/trace/trace-categories";

export type TraceDetail = {
  request_id?: string;
  session_id?: string;
  tenant?: string;
  provider?: string;
  model_requested?: string;
  stream?: boolean;
  agent_type?: string;
  status_code?: number;
  stop_reason?: string;
  n_messages?: number;
  n_tool_use?: number;
  created_at?: string;
  messages?: unknown;
  request_raw?: unknown;
  response_raw?: unknown;
  error_raw?: string;
};

/**
 * Trace detail client (presentational): renders the messages or raw view for a
 * request_id. The detail is fetched server-side (RSC) via serverAdminClient —
 * the audited read (ADR-0039 §5) and the BFF auth flow both happen on the
 * server. This component only owns client-side concerns (the copy button and
 * the messages/raw tab).
 *
 * The Messages view delegates to the shared TraceCategories component (6
 * segments: system / tools / user / assistant / carried-over / output). The
 * `previous` detail is the chronological predecessor in the same session,
 * needed to derive the carried-over segment; null for the first request.
 */
export function TraceDetailClient({
  sessionID,
  requestID,
  view,
  current,
  previous,
}: {
  sessionID: string;
  requestID: string;
  view: "messages" | "raw";
  current: TraceDetail | null;
  previous: TraceDetail | null;
}) {
  const t = useTranslations("trace");

  const base = `/trace/sessions/${encodeURIComponent(sessionID)}/${encodeURIComponent(requestID)}`;

  return (
    <>
      <Button
        href={`/trace/sessions/${encodeURIComponent(sessionID)}`}
        variant="outline"
        size="sm"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        {t("detail.backToSession")}
      </Button>

      <div className="flex flex-col gap-2">
        <h1 className="text-xl font-semibold text-foreground">
          {view === "messages" ? t("messages.title") : t("raw.title")}
        </h1>
        <p className="text-sm text-muted-foreground">
          {t("detail.requestId")}: <span className="font-mono">{requestID}</span>
          {current?.agent_type ? (
            <span className="ml-3">
              {t("agent.type")}: <span className="text-foreground">{current.agent_type}</span>
            </span>
          ) : null}
        </p>
        <p className="text-sm text-muted-foreground">
          {view === "messages" ? t("messages.subtitle") : t("raw.subtitle")}
        </p>
        {/* View switcher */}
        <div className="flex gap-2">
          <Button
            href={`${base}/messages`}
            variant={view === "messages" ? "primary" : "outline"}
            size="sm"
          >
            {t("messages.title")}
          </Button>
          <Button
            href={`${base}/raw`}
            variant={view === "raw" ? "primary" : "outline"}
            size="sm"
          >
            {t("raw.title")}
          </Button>
        </div>
      </div>

      {!current ? (
        <p className="text-sm text-muted-foreground">{t("detail.notFound")}</p>
      ) : view === "messages" ? (
        <TraceCategories current={current} previous={previous} t={t} />
      ) : (
        <RawView detail={current} t={t} />
      )}
    </>
  );
}

function RawView({
  detail,
  t,
}: {
  detail: TraceDetail;
  t: ReturnType<typeof useTranslations>;
}) {
  return (
    <div className="flex flex-col gap-4">
      <RawBlock label={t("raw.request")} value={detail.request_raw} t={t} />
      <RawBlock label={t("raw.response")} value={detail.response_raw} t={t} text />
      {detail.error_raw ? (
        <RawBlock label={t("raw.error")} value={detail.error_raw} t={t} text />
      ) : null}
    </div>
  );
}

function RawBlock({
  label,
  value,
  t,
  text,
}: {
  label: string;
  value: unknown;
  t: ReturnType<typeof useTranslations>;
  text?: boolean;
}) {
  const [copied, setCopied] = useState(false);
  const isEmpty =
    value === null ||
    value === undefined ||
    value === "" ||
    (typeof value === "object" && Object.keys(value as object).length === 0);
  const content = text ? String(value) : JSON.stringify(value ?? null, null, 2);

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">{label}</h3>
        {!isEmpty && (
          <button
            type="button"
            className="rounded px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent"
            onClick={() => {
              navigator.clipboard?.writeText(content);
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            }}
          >
            {copied ? t("raw.copied") : t("raw.copy")}
          </button>
        )}
      </div>
      <pre className="max-h-[60vh] overflow-auto rounded-lg border border-border bg-muted/40 p-3 text-xs">
        {isEmpty ? t("raw.empty") : content}
      </pre>
    </div>
  );
}
