#!/usr/bin/env node
// check-i18n-keys.mjs — verify every static t("key") call resolves to a real
// message in the locale files. Catches the class of bug where code references a
// translation key (e.g. `t("filters.modelRequested")`) that was never added to
// the JSON, or where the namespace string does not match the filename (e.g.
// `useTranslations("configHistory")` vs file `config-history.json`).
//
// Scope: STATIC calls only. Dynamic keys are skipped:
//   - template literals:  t(`prefix.${var}`)   — the prefix is not enumerated
//   - variable args:      t(state.errorKey)    — resolved at runtime
// Only `t("literal")` and `t('literal')` are checked.
//
// Approach: mirror web/src/i18n/request.ts to build the message tree from
// src/locales/en/, then walk every .ts/.tsx under web/src, build a per-file map
// of {aliasVar -> namespace} from useTranslations/getTranslations calls, and
// validate each static alias call against the namespace subtree.
//
// Usage: node scripts/check-i18n-keys.mjs   (run from repo root)
// Exits 0 if all static keys resolve, 1 otherwise, 2 on environment error.
// Zero runtime dependencies — Node built-ins only.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const WEB = "web";
const LOCALE_DIR = join(WEB, "src", "locales", "en");
const SRC_DIR = join(WEB, "src");

// --- 1. Build the namespace message tree, identical to request.ts -----------
// A file at locales/en/errors/auth.json mounts at messages.errors.auth.
function discoverJson(dir, prefix, out) {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    const st = statSync(full);
    if (st.isDirectory()) {
      discoverJson(full, prefix + name + ".", out);
    } else if (st.isFile() && name.endsWith(".json")) {
      out.push(prefix + name.slice(0, -".json".length));
    }
  }
  return out;
}

try {
  statSync(LOCALE_DIR);
} catch {
  console.error(`check-i18n-keys: ${LOCALE_DIR} not found`);
  process.exit(2);
}

const namespaces = discoverJson(LOCALE_DIR, "", []).sort();
const messages = {};
for (const ns of namespaces) {
  const parts = ns.split(".");
  const filePath = join(LOCALE_DIR, ...parts) + ".json";
  let mod;
  try {
    mod = JSON.parse(readFileSync(filePath, "utf8"));
  } catch (e) {
    console.error(`check-i18n-keys: failed to parse ${filePath}: ${e.message}`);
    process.exit(2);
  }
  let cursor = messages;
  for (let i = 0; i < parts.length - 1; i++) {
    if (cursor[parts[i]] === undefined) cursor[parts[i]] = {};
    cursor = cursor[parts[i]];
  }
  cursor[parts[parts.length - 1]] = mod;
}

// Resolve a dotted key path against an object; returns true if a string leaf
// (or any value) exists at that path.
function hasKey(obj, dotted) {
  const parts = dotted.split(".");
  let cur = obj;
  for (const p of parts) {
    if (cur === null || typeof cur !== "object" || !(p in cur)) return false;
    cur = cur[p];
  }
  return true;
}

// --- 2. Walk every source file under web/src --------------------------------
function walkTs(dir, out) {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    const st = statSync(full);
    if (st.isDirectory()) {
      walkTs(full, out);
    } else if (st.isFile() && (name.endsWith(".ts") || name.endsWith(".tsx"))) {
      // Skip test files — they exercise the ICU parser, not real t() calls.
      if (name.endsWith(".test.ts") || name.endsWith(".test.tsx")) continue;
      out.push(full);
    }
  }
  return out;
}

const files = walkTs(SRC_DIR, []);

let failures = 0;
let checked = 0;
let skippedDynamic = 0;

// Match: <lhs> = useTranslations("ns")  OR  <lhs> = getTranslations("ns")
// Captures the alias variable name and the namespace string. Allows the hook
// to be awaited (getTranslations) or not (useTranslations).
const nsRe = /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:await\s+)?(?:useTranslations|getTranslations)\s*\(\s*("([^"]*)"|'([^']*)')\s*\)/g;
// Match a call on the alias: alias( ARG ) where ARG starts with a quote
// (static, group 2/3 capture the literal) or a backtick/variable (dynamic,
// no capture — caller detects via whether a literal was captured).
const callRe = (alias) =>
  new RegExp(
    "\\b" +
      alias.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") +
      '\\s*\\(\\s*("([^"]*)"|\'([^\']*)\'|.)',
    "g",
  );

for (const file of files) {
  const src = readFileSync(file, "utf8");
  const rel = relative(".", file);

  // Build alias -> namespace map for this file.
  const aliasToNs = new Map();
  let m;
  nsRe.lastIndex = 0;
  while ((m = nsRe.exec(src)) !== null) {
    const alias = m[1];
    const ns = m[3] ?? m[4]; // double-quoted (group 3) or single-quoted (group 4)
    aliasToNs.set(alias, ns);
  }
  if (aliasToNs.size === 0) continue; // no translations in this file

  // For each alias, scan its static calls.
  for (const [alias, ns] of aliasToNs) {
    // Validate the namespace itself exists as a top-level subtree.
    if (!hasKey(messages, ns)) {
      // Report every static call against the bad namespace so the author sees
      // the blast radius; collapse duplicate namespaces via a seen-set below.
      const calls = [];
      const cre = callRe(alias);
      cre.lastIndex = 0;
      let cm;
      while ((cm = cre.exec(src)) !== null) {
        const quoted = cm[1];
        // Static only: a double- or single-quoted literal was captured.
        if (!quoted.startsWith('"') && !quoted.startsWith("'")) {
          skippedDynamic++;
          continue;
        }
        const key = cm[2] ?? cm[3];
        calls.push(key);
      }
      if (calls.length === 0) continue;
      const line = src.slice(0, src.indexOf(`useTranslations("${ns}")`)).split("\n").length;
      console.error(
        `check-i18n-keys: ${rel}: namespace "${ns}" (used by ${alias}) has no locale file; ${calls.length} static call(s) will render raw keys`,
      );
      failures++;
      checked += calls.length;
      continue;
    }

    const subtree = ns.split(".").reduce((o, k) => o?.[k], messages);
    const cre = callRe(alias);
    cre.lastIndex = 0;
    let cm;
    while ((cm = cre.exec(src)) !== null) {
      const quoted = cm[1];
      // Static only: a double- or single-quoted literal was captured.
      if (!quoted.startsWith('"') && !quoted.startsWith("'")) {
        skippedDynamic++;
        continue;
      }
      const key = cm[2] ?? cm[3];
      checked++;
      if (!hasKey(subtree, key)) {
        const lineNo = src.slice(0, cm.index).split("\n").length;
        console.error(
          `check-i18n-keys: ${rel}:${lineNo}: ${alias}("${key}") not found in namespace "${ns}"`,
        );
        failures++;
      }
    }
  }
}

// --- 3. Summary -------------------------------------------------------------
if (failures === 0) {
  console.log(
    `check-i18n-keys: OK (${checked} static keys resolved across ${files.length} files, ${skippedDynamic} dynamic skipped)`,
  );
} else {
  console.error(`check-i18n-keys: FAIL — ${failures} unresolved key(s), see above`);
}
process.exit(failures === 0 ? 0 : 1);
