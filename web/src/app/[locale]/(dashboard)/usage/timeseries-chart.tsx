"use client";

import { useTranslations } from "next-intl";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { microToDisplay } from "@/lib/money";

type Bucket = {
  bucket_start?: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  cost?: number;
  request_count?: number;
};

/**
 * Cost/token trend chart for the usage page. Renders a recharts AreaChart of
 * the time-bucketed usage series. Empty buckets (omitted by the backend) are
 * simply absent — the chart connects existing points.
 */
export function UsageTimeseriesChart({ rows }: { rows: Bucket[] }) {
  const t = useTranslations("usage");

  if (rows.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-background p-4">
        <h2 className="mb-2 text-sm font-semibold text-foreground">
          {t("chart.costTrend")}
        </h2>
        <p className="py-8 text-center text-sm text-muted-foreground">
          {t("chart.noData")}
        </p>
      </div>
    );
  }

  const data = rows.map((r) => {
    const d = r.bucket_start ? new Date(r.bucket_start) : new Date();
    const hasSubDayTime =
      r.bucket_start && (d.getHours() !== 0 || d.getMinutes() !== 0);
    const label = hasSubDayTime
      ? d.toLocaleDateString(undefined, { month: "short", day: "numeric" }) +
        " " +
        d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
      : d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    return {
      label,
      cost: r.cost ?? 0,
      tokens: (r.prompt_tokens ?? 0) + (r.completion_tokens ?? 0),
      requests: r.request_count ?? 0,
    };
  });

  return (
    <div className="rounded-lg border border-border bg-background p-4">
      <h2 className="mb-3 text-sm font-semibold text-foreground">
        {t("chart.costTrend")}
      </h2>
      <ResponsiveContainer width="100%" height={260}>
        <AreaChart data={data} margin={{ top: 5, right: 10, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="costGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--primary)" stopOpacity={0.3} />
              <stop offset="95%" stopColor="var(--primary)" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
          <XAxis
            dataKey="label"
            tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
            tickLine={false}
            axisLine={{ stroke: "var(--border)" }}
          />
          <YAxis
            tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
            tickLine={false}
            axisLine={false}
            width={50}
          />
          <Tooltip
            contentStyle={{
              backgroundColor: "var(--popover)",
              border: "1px solid var(--border)",
              borderRadius: "6px",
              fontSize: "12px",
            }}
            labelStyle={{ color: "var(--foreground)" }}
            formatter={(value, name) => {
              const v = Number(value);
              if (name === "cost") return [microToDisplay(v), t("chart.cost")];
              if (name === "tokens") return [v.toLocaleString(), t("chart.tokens")];
              return [String(value), String(name)];
            }}
          />
          <Area
            type="monotone"
            dataKey="cost"
            stroke="var(--primary)"
            strokeWidth={2}
            fill="url(#costGrad)"
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
