import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "@formatjs/icu-messageformat-parser";

/**
 * Validate that every locale message string parses as valid ICU MessageFormat.
 *
 * next-intl parses messages at runtime via @formatjs/icu-messageformat-parser;
 * a literal `{` in a translation value (e.g. a JSON example like `{"rpm": 100}`)
 * is treated as the start of an ICU argument and throws MALFORMED_ARGUMENT when
 * the inner text doesn't match `{name, type, format}` syntax. `check-i18n.sh`
 * only verifies key alignment across locales — it does not catch malformed ICU.
 * This test fills that gap so the bug class is caught in CI before runtime.
 */
const LOCALES_ROOT = fileURLToPath(new URL("./", import.meta.url));

function listJsonFiles(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      listJsonFiles(full, acc);
    } else if (entry.endsWith(".json")) {
      acc.push(full);
    }
  }
  return acc;
}

function collectStrings(
  value: unknown,
  prefix: string,
  out: Array<{ key: string; value: string }>,
): void {
  if (typeof value === "string") {
    out.push({ key: prefix, value });
  } else if (value !== null && typeof value === "object") {
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      collectStrings(v, prefix ? `${prefix}.${k}` : k, out);
    }
  }
}

const locales = readdirSync(LOCALES_ROOT).filter(
  (entry) => entry !== "messages.test.ts" && statSync(join(LOCALES_ROOT, entry)).isDirectory(),
);

describe("locale messages: ICU syntax validity", () => {
  for (const locale of locales) {
    const files = listJsonFiles(join(LOCALES_ROOT, locale));
    const cases: Array<{ ns: string; key: string; value: string }> = [];
    for (const file of files) {
      const ns = relative(join(LOCALES_ROOT, locale), file)
        .replace(/\.json$/, "")
        .split(sep)
        .join(".");
      const data = JSON.parse(readFileSync(file, "utf-8"));
      const strings: Array<{ key: string; value: string }> = [];
      collectStrings(data, "", strings);
      for (const { key, value } of strings) {
        cases.push({ ns, key, value });
      }
    }

    for (const { ns, key, value } of cases) {
      it(`${locale}/${ns}.${key} parses as valid ICU`, () => {
        expect(() => parse(value)).not.toThrow();
      });
    }
  }
});
