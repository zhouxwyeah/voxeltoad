"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui";

export default function Error({ reset }: { reset: () => void }) {
  const t = useTranslations("applications.state");
  return <div className="flex flex-col items-start gap-3 p-8">
    <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{t("error")}</p>
    <Button variant="outline" onClick={reset}>{t("retry")}</Button>
  </div>;
}
