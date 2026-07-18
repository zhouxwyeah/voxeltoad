#!/usr/bin/env bash
# check-errors.sh — validate the internal/apperr error catalog.
#
# For every apperr.New("code", status, "i18n") call in internal/apperr/*.go,
# verifies:
#   1. The error code is unique across the catalog.
#   2. The code is snake_case (lowercase, underscore-separated).
#   3. The HTTP status is a valid 4xx/5xx.
#   4. The i18n key "errors.<domain>.<key>" resolves in
#      web/src/locales/en/errors/<domain>.json.
#
# Usage: ./scripts/check-errors.sh
# Run from repo root. Exits 0 if valid, 1 otherwise. Requires grep + jq.

set -euo pipefail

APPERR_DIR="internal/apperr"
LOCALES_EN="web/src/locales/en/errors"
LOCALES_ZH="web/src/locales/zh/errors"

if [ ! -d "$APPERR_DIR" ]; then
  echo "check-errors: $APPERR_DIR not found" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "check-errors: jq is required but not installed" >&2
  exit 2
fi

# Extract every `New("code", status, "i18n")` call as one match per line.
# The pattern must capture the closing ) so the full call is one match.
entries=$(grep -hoE 'New\("[a-z0-9_]+",\s*Status[A-Za-z]+,\s*"errors\.[a-z]+\.[a-zA-Z]+"\)' "$APPERR_DIR"/*.go 2>/dev/null || true)

if [ -z "$entries" ]; then
  echo "check-errors: no apperr.New() calls found in $APPERR_DIR" >&2
  exit 2
fi

exit_code=0
seen_codes=""

# grep -o emits one match per line; read line-by-line to survive spaces in
# matches (there are none here, but the quoted strings could contain spaces in
# future messages).
while IFS= read -r entry; do
  [ -z "$entry" ] && continue
  # Parse: New("code", Status, "errors.domain.key")
  # BSD sed (macOS) doesn't support \s; use [[:space:]].
  code=$(echo "$entry" | sed -n 's|.*New("\([a-z0-9_]*\)".*|\1|p')
  status=$(echo "$entry" | sed -n 's|.*,[[:space:]]*\(Status[A-Za-z]*\),.*|\1|p')
  i18n=$(echo "$entry" | sed -n 's|.*"errors\.\([a-zA-Z.]*\)".*|errors.\1|p')

  if [ -z "$code" ] || [ -z "$status" ] || [ -z "$i18n" ]; then
    echo "check-errors: failed to parse entry: $entry" >&2
    exit_code=1
    continue
  fi

  # 1. Uniqueness.
  if echo "$seen_codes" | grep -q "|$code|"; then
    echo "check-errors: duplicate code $code" >&2
    exit_code=1
  else
    seen_codes="$seen_codes|$code"
  fi

  # 2. snake_case (regex already enforced by the grep filter, double-check).
  if ! echo "$code" | grep -qE '^[a-z][a-z0-9_]*$'; then
    echo "check-errors: code $code is not snake_case" >&2
    exit_code=1
  fi

  # 3. HTTP status validity (map StatusXxx → number, must be 4xx/5xx).
  case "$status" in
    StatusBadRequest|StatusUnauthorized|StatusForbidden|StatusNotFound|StatusConflict|StatusTooManyRequests|StatusPaymentRequired|StatusInternalServerError|StatusBadGateway)
      ;;
    *)
      echo "check-errors: code $code has unknown/invalid status $status" >&2
      exit_code=1
      ;;
  esac

  # 4. i18n key exists in errors/<domain>.json (both en and zh).
  domain=${i18n#errors.}
  domain=${domain%%.*}
  key=${i18n#errors.${domain}.}
  for locale_dir in "$LOCALES_EN" "$LOCALES_ZH"; do
    json_file="$locale_dir/$domain.json"
    if [ ! -f "$json_file" ]; then
      echo "check-errors: code $code → i18n $i18n but $json_file missing" >&2
      exit_code=1
      continue
    fi
    if ! jq -e --arg k "$key" 'has($k)' "$json_file" >/dev/null 2>&1; then
      echo "check-errors: code $code → i18n key $key not found in $json_file" >&2
      exit_code=1
    fi
  done
done <<< "$entries"

if [ "$exit_code" -eq 0 ]; then
  count=$(echo "$seen_codes" | tr '|' '\n' | grep -c .)
  echo "check-errors: OK ($count error codes validated)"
else
  echo "check-errors: FAIL — see above" >&2
fi

exit "$exit_code"
