#!/usr/bin/env bash
# check-i18n.sh — verify locale key alignment across all supported locales.
#
# For each namespace JSON in src/locales/en/, recursively collect the full
# dotted key set (paths-to-scalars) and compare it against every other locale
# (zh, ...). Fail if any locale is missing or has extra keys vs the en baseline.
#
# en is the source of truth (default locale); other locales must mirror its
# key shape exactly. Value text may differ; keys must not.
#
# Usage: ./scripts/check-i18n.sh
# Run from repo root. Exits 0 if aligned, 1 otherwise. Requires jq.

set -euo pipefail

LOCALES_DIR="web/src/locales"
DEFAULT_LOCALE="en"

if ! command -v jq >/dev/null 2>&1; then
  echo "check-i18n: jq is required but not installed" >&2
  exit 2
fi

if [ ! -d "$LOCALES_DIR/$DEFAULT_LOCALE" ]; then
  echo "check-i18n: default locale dir $LOCALES_DIR/$DEFAULT_LOCALE not found" >&2
  exit 2
fi

# Collect the set of locales (subdirectories) to check against the default.
locales=$(find "$LOCALES_DIR" -maxdepth 1 -mindepth 1 -type d -exec basename {} \; | sort)

# Collect namespace files from the default locale, recursively, as dotted
# relative paths (e.g. "errors/auth.json" → "errors.auth"). This mirrors the
# namespace discovery in web/src/i18n/request.ts.
namespaces=$(cd "$LOCALES_DIR/$DEFAULT_LOCALE" && find . -name '*.json' -print | sed 's|^\./||; s|/|.|g; s|\.json$||' | sort)

ns_count=$(printf '%s\n' "$namespaces" | grep -c .)
if [ "$ns_count" -eq 0 ]; then
  echo "check-i18n: no namespace JSONs found in $LOCALES_DIR/$DEFAULT_LOCALE" >&2
  exit 2
fi

exit_code=0

for ns in $namespaces; do
  # Convert dotted namespace back to a relative file path.
  rel_path=$(echo "$ns" | sed 's|\.|/|g').json
  base_file="$LOCALES_DIR/$DEFAULT_LOCALE/$rel_path"
  base_keys=$(jq -r 'paths(scalars) | join(".")' "$base_file" | sort)

  for loc in $locales; do
    [ "$loc" = "$DEFAULT_LOCALE" ] && continue
    other_file="$LOCALES_DIR/$loc/$rel_path"

    if [ ! -f "$other_file" ]; then
      echo "check-i18n: $loc/$rel_path missing (expected by $DEFAULT_LOCALE/$rel_path)" >&2
      exit_code=1
      continue
    fi

    other_keys=$(jq -r 'paths(scalars) | join(".")' "$other_file" | sort)

    if [ "$base_keys" != "$other_keys" ]; then
      echo "check-i18n: key mismatch in $loc/$rel_path vs $DEFAULT_LOCALE/$rel_path" >&2
      diff <(echo "$base_keys") <(echo "$other_keys") | sed 's/^/  /' >&2
      exit_code=1
    fi
  done
done

loc_count=$(printf '%s\n' "$locales" | grep -c .)
if [ "$exit_code" -eq 0 ]; then
  echo "check-i18n: OK ($ns_count namespaces, $loc_count locales aligned)"
else
  echo "check-i18n: FAIL — locale keys out of sync (see above)" >&2
fi

exit "$exit_code"
