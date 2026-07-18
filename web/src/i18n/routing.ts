import { defineRouting } from "next-intl/routing";

export const routing = defineRouting({
  // Supported locales
  locales: ["en", "zh"],
  // Default locale (used when no locale prefix)
  defaultLocale: "en",
  // Default locale prefix is not shown in URL
  localePrefix: "as-needed",
});
