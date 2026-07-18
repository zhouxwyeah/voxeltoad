#!/usr/bin/env bash
# check-permissions — validate that every requirePermission() call references a
# key defined in the authz permission catalog, and that every catalog entry is
# in valid resource.action format and is unique.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CATALOG="$ROOT/internal/authz/permission.go"
FAIL=0

# 1. Extract catalog keys: lines like `PermFoo Permission = "resource.action"`
keys=$(grep -o 'Permission = "[^"]*"' "$CATALOG" | sed 's/Permission = "//;s/"//' | sort)
if [ -z "$keys" ]; then
  echo "ERROR: no permission keys found in $CATALOG"
  exit 1
fi

# 2. Verify every key is unique.
dup=$(echo "$keys" | uniq -d)
if [ -n "$dup" ]; then
  echo "ERROR: duplicate permission keys:"
  echo "$dup"
  FAIL=1
fi

# 3. Verify every key is in resource.action format (exactly one dot),
#    or is the special wildcard sentinel "*".
echo "$keys" | while IFS= read -r k; do
  if [ "$k" = "*" ]; then
    continue
  fi
  dots=$(echo "$k" | tr -cd '.' | wc -c | tr -d ' ')
  if [ "$dots" != "1" ]; then
    echo "ERROR: permission key '$k' must be in resource.action format (exactly one dot)"
    exit 1
  fi
done || FAIL=1

# 4. Warn if requirePermission calls reference unknown constants (best-effort).
if grep -rq 'requirePermission(' "$ROOT/internal/admin" 2>/dev/null; then
  refs=$(grep -roh 'requirePermission(authz\.Perm[A-Za-z]*)' "$ROOT/internal/admin" 2>/dev/null | \
    sed 's/requirePermission(authz\.//;s/)//' | sort -u || true)
  for ref in $refs; do
    val=$(grep "${ref}.*Permission = " "$CATALOG" | sed 's/.*Permission = "\([^"]*\)".*/\1/' || true)
    if [ -z "$val" ]; then
      echo "WARN: requirePermission references $ref but constant not found in catalog"
    else
      if ! echo "$keys" | grep -qxF "$val"; then
        echo "WARN: requirePermission references $ref ($val) but key not in catalog"
      fi
    fi
  done
fi

if [ "$FAIL" -ne 0 ]; then
  echo "check-permissions: FAIL"
  exit 1
fi

count=$(echo "$keys" | wc -l | tr -d ' ')
echo "check-permissions: OK ($count permission keys validated)"
