"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";

export default function BudgetError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  const t = useTranslations("budgets");
  return <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8"><h1 className="text-xl font-semibold text-foreground">{t("heading")}</h1><p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{t("loadError")}</p><div className="flex gap-2"><Button onClick={reset} variant="outline">{t("actions.retry")}</Button><Button href="/budgets" variant="outline">{t("back")}</Button></div></div>;
}
