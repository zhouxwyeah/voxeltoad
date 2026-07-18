import fs from "node:fs";
import path from "node:path";
import { getRequestConfig } from "next-intl/server";
import { routing } from "./routing";

// Recursively discover namespace JSON files from the default locale directory
// at module load time. A file at src/locales/en/errors/auth.json becomes the
// dotted namespace "errors.auth". This avoids a hardcoded namespace list that
// every parallel worktree has to edit on the same line: adding a new resource
// page or a new error domain only requires dropping a new JSON file, no edit
// here.
function discoverNamespaces(dir: string, prefix: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...discoverNamespaces(full, prefix + entry.name + "."));
    } else if (entry.isFile() && entry.name.endsWith(".json")) {
      out.push(prefix + entry.name.slice(0, -".json".length));
    }
  }
  return out;
}

const namespaces = discoverNamespaces(
  path.join(process.cwd(), "src/locales/en"),
  "",
).sort();

export default getRequestConfig(async ({ requestLocale }) => {
  let locale = await requestLocale;

  // Ensure the locale is valid; fall back to default if not.
  if (
    !locale ||
    !routing.locales.includes(locale as (typeof routing.locales)[number])
  ) {
    locale = routing.defaultLocale;
  }

  // Load all namespace JSON files into the messages object as a nested tree.
  // A dotted namespace "errors.auth" becomes messages.errors.auth.
  const messages: Record<string, unknown> = {};

  for (const ns of namespaces) {
    const parts = ns.split(".");
    const filePath = `../locales/${locale}/${parts.join("/")}.json`;
    // eslint-disable-next-line no-await-in-loop
    const mod = await import(filePath);
    let cursor = messages;
    for (let i = 0; i < parts.length - 1; i++) {
      const k = parts[i];
      if (cursor[k] === undefined) cursor[k] = {};
      cursor = cursor[k] as Record<string, unknown>;
    }
    cursor[parts[parts.length - 1]] = mod.default;
  }

  return {
    locale,
    messages,
  };
});
