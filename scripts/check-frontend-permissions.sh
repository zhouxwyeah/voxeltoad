#!/usr/bin/env bash
# check-frontend-permissions.sh — verify frontend nav permission strings
# match the backend permission catalog.
#
# Catches the class of drift where a backend permission is renamed/removed
# but the frontend nav still gates on the old string (silent 403 for users).
#
# Usage: ./scripts/check-frontend-permissions.sh
# Run from repo root. Exits 0 if valid, 1 on mismatch.

set -euo pipefail

BACKEND_CATALOG="internal/authz/permission.go"
FRONTEND_NAV="web/src/lib/nav-perms.ts"

for f in "$BACKEND_CATALOG" "$FRONTEND_NAV"; do
  if [ ! -f "$f" ]; then
    echo "check-frontend-permissions: $f not found" >&2
    exit 2
  fi
done

# Extract backend permission strings: lines like `PermXxx Permission = "xxx.yyy"`.
backend_perms=$(grep -oE 'Permission\s*=\s*"[a-z_]+\.[a-z_]+"' "$BACKEND_CATALOG" \
  | sed -E 's/.*"([a-z_]+\.[a-z_]+)"/\1/' \
  | sort -u)

# Extract frontend nav permission strings: values in NAV_PERMS object.
frontend_perms=$(grep -oE '"[a-z_]+\.[a-z_]+"' "$FRONTEND_NAV" \
  | sed 's/"//g' \
  | sort -u)

# Frontend perms must be a subset of backend perms.
missing_in_backend=$(comm -23 <(printf '%s\n' "$frontend_perms") <(printf '%s\n' "$backend_perms"))

if [ -n "$missing_in_backend" ]; then
  echo "check-frontend-permissions: frontend nav permissions not in backend catalog:" >&2
  echo "$missing_in_backend" | sed 's/^/  /' >&2
  exit 1
fi

echo "check-frontend-permissions: OK ($(echo "$frontend_perms" | wc -l | tr -d ' ') nav permissions all present in backend catalog)"
exit 0
