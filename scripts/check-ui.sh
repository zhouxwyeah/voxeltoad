#!/usr/bin/env bash
# check-ui.sh — enforce design-system.md §1.2 hard rules (the UI no-drift gate).
#
# grep-based, same style as check-i18n.sh. Scans web/src + desktop-ui/src for:
#   1. Hex color literals in ts/tsx (colors must come from tokens).
#   2. Rainbow/gray scale utilities (bg-blue-*, text-emerald-*, …) outside the
#      allowlist — the UI palette is brand blue + neutrals + semantic colors.
#   3. dark: variants (the design is light-only, they are dead code).
#   4. Native alert()/confirm()/window.location.reload (use ConfirmModal/toast
#      + router.refresh()).
#   5. Emoji / pictograph icons (🔧 ⚠ ● ▾ ▸ ← …) — lucide-react is the only
#      icon source.
#   6. Native <select> outside the allowlist (use ui/select.tsx).
#   7. Scaffold asset references (next.svg / vercel.svg / …).
#
# Allowlists are file-level and SHRINK-ONLY (design-system.md §8/§9): every
# entry corresponds to a registered convergence item; removing an entry means
# the file was cleaned. Adding one requires a design-system.md §8 row.
#
# Usage: ./scripts/check-ui.sh
# Run from repo root. Exits 0 if clean, 1 on violations, 2 on env error.

set -euo pipefail

SCAN_DIRS=(web/src desktop-ui/src)
for d in "${SCAN_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-ui: $d not found (run from repo root)" >&2
    exit 2
  fi
done

# --- allowlists (shrink-only, see design-system.md §8) -----------------------

# Rainbow/gray scale utilities still present in these trace renderers
# (13-hue palettes converge to the semantic board in the P1 batch).
RAINBOW_ALLOWLIST=(
  "web/src/components/trace/trace-categories.tsx"
  "desktop-ui/src/components/trace/trace-categories.tsx"
  "desktop-ui/src/components/trace/json-tree.tsx"
)

# Native <select> still in use: two Select primitive internals (allowed by
# construction) + four filter-bar/pager migration items (design-system.md §8).
SELECT_ALLOWLIST=(
  "web/src/components/ui/select.tsx"
  "desktop-ui/src/components/ui/select.tsx"
  "web/src/components/ui/pagination.tsx"
  "web/src/app/[locale]/(dashboard)/trace/client.tsx"
  "web/src/app/[locale]/(dashboard)/request-logs/client.tsx"
  "web/src/app/[locale]/(dashboard)/usage/client.tsx"
)

files=$(find "${SCAN_DIRS[@]}" -type f \( -name '*.tsx' -o -name '*.ts' \) | sort)

exit_code=0

# check_rule <name> <pattern> [exempt-file...]
# Prints every match as "check-ui: [name] file:line:text" and marks failure.
check_rule() {
  local name="$1" pattern="$2"
  shift 2
  local -a exempt=("$@")
  local -a targets=()
  local f x skip
  while IFS= read -r f; do
    skip=0
    for x in "${exempt[@]}"; do
      if [ "$f" = "$x" ]; then skip=1; break; fi
    done
    [ "$skip" = "0" ] && targets+=("$f")
  done <<< "$files"
  [ "${#targets[@]}" -eq 0 ] && return 0
  local hits
  hits=$(grep -nHE "$pattern" "${targets[@]}" || true)
  if [ -n "$hits" ]; then
    printf '%s\n' "$hits" | sed "s|^|check-ui: [$name] |" >&2
    exit_code=1
  fi
}

# 1. hex color literals (tokens only; globals.css/index.css are not scanned)
check_rule "no-hex-literal" '#[0-9a-fA-F]{3,8}([^0-9a-zA-Z]|$)'

# 2. rainbow / gray scale utilities (semantic board only; §1.2-3)
check_rule "no-rainbow-scale" \
  '(bg|text|border|from|via|to|ring|fill|stroke)-(slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-[0-9]' \
  "${RAINBOW_ALLOWLIST[@]}"

# 3. dark: variants (light-only design)
check_rule "no-dark-variant" 'dark:'

# 4. native dialogs / hard reload
check_rule "no-native-dialog" '(^|[^A-Za-z_])(window\.)?(alert|confirm)[[:space:]]*\('
check_rule "no-hard-reload" 'window\.location\.reload'

# 5. emoji / pictograph icons (lucide-react only). Arrows like → are excluded:
#    they are legitimate typography in prose and comments, not icons.
check_rule "no-emoji-icon" '🔧|⚠|●|▾|▸|◂|✓|✗|✅|❌|⭐|🚨|💡|📊|📈|🔍|⏳|❓'

# 6. native <select> (use ui/select.tsx)
check_rule "no-native-select" '<select[[:space:]>]' \
  "${SELECT_ALLOWLIST[@]}"

# 7. scaffold assets (deleted 2026-07; keep them out)
check_rule "no-scaffold-asset" '(next|vercel|file|globe|window)\.svg'

file_count=$(printf '%s\n' "$files" | grep -c .)
if [ "$exit_code" -eq 0 ]; then
  echo "check-ui: OK ($file_count files, 8 rules, ${#RAINBOW_ALLOWLIST[@]}+${#SELECT_ALLOWLIST[@]} allowlisted)"
else
  echo "check-ui: FAIL — design-system.md §1.2 violation(s) above" >&2
fi

exit "$exit_code"
