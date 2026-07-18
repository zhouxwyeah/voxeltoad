#!/usr/bin/env bash
# check-docs.sh — verify documentation consistency.
#
# Catches the classes of doc drift found during the 2026-07 health audit:
#   1. ADR index vs. file mismatch (missing/duplicate/misaligned numbers).
#   2. ADR filename not matching NNNN-kebab-title.md.
#   3. A migration-created table never mentioned in design/database.md.
#   4. Stale "deferred" wording in living docs (design/ + CODEBUDDY.md).
#      ADRs are excluded — they are immutable history where "deferred to
#      phase 2" is permanently correct. Check 4 is advisory by default and
#      only fails under CHECK_DOCS_STRICT=1.
#
# Usage: ./scripts/check-docs.sh
# Run from repo root. Exits 0 if valid, 1 on a hard failure, 2 on an
# environment error. Requires only coreutils + grep/sed/awk.

set -euo pipefail

ADR_DIR="docs/adr"
ADR_INDEX="$ADR_DIR/README.md"
MIGRATIONS_DIR="internal/store/migrations"
DB_DOC="design/database.md"
LIVING_DOCS=(design/*.md CODEBUDDY.md)

for d in "$ADR_DIR" "$MIGRATIONS_DIR"; do
  if [ ! -d "$d" ]; then
    echo "check-docs: $d not found" >&2
    exit 2
  fi
done
if [ ! -f "$ADR_INDEX" ]; then
  echo "check-docs: $ADR_INDEX not found" >&2
  exit 2
fi
if [ ! -f "$DB_DOC" ]; then
  echo "check-docs: $DB_DOC not found" >&2
  exit 2
fi

exit_code=0
deferred_reviews=0

# ---------------------------------------------------------------- check 1
# ADR index vs. file consistency. Two sorted number lists are diffed:
#   file_nums  — the NNNN prefix of every docs/adr/NNNN-*.md file
#   index_nums — every NNNN appearing as the link text in the README table
# Equality implies: same count, no duplicates on either side, no misalignment.
file_nums=$(ls "$ADR_DIR" 2>/dev/null \
  | grep -E '^[0-9]{4}-' \
  | sed -E 's/^([0-9]{4})-.*/\1/' \
  | sort)
index_nums=$(grep -oE '\[[0-9]{4}\]' "$ADR_INDEX" \
  | sed -E 's/^\[([0-9]{4})\]$/\1/' \
  | sort)

if [ "$file_nums" != "$index_nums" ]; then
  echo "check-docs: ADR index/file number mismatch (diff: < only-in-files, > only-in-index)" >&2
  diff <(printf '%s\n' "$file_nums") <(printf '%s\n' "$index_nums") | sed 's/^/  /' >&2
  exit_code=1
fi

# ---------------------------------------------------------------- check 2
# ADR filename convention: NNNN-lowercase-kebab.md.
bad_names=$(ls "$ADR_DIR" 2>/dev/null \
  | grep -E '^[0-9]{4}-' \
  | grep -vE '^[0-9]{4}-[a-z0-9-]+\.md$' \
  || true)
if [ -n "$bad_names" ]; then
  while IFS= read -r name; do
    [ -z "$name" ] && continue
    echo "check-docs: ADR filename '$name' does not match NNNN-kebab-title.md" >&2
  done <<< "$bad_names"
  exit_code=1
fi

# ---------------------------------------------------------------- check 3
# Every business table created by a migration must be mentioned (backticked)
# in design/database.md at least once. Partition children, the goose bookkeeping
# table, dynamic PARTITION-of templates, and SQL comments are excluded.
# Match only real DDL: CREATE TABLE [IF NOT EXISTS] name ( — the trailing "("
# anchors a genuine table definition and rules out prose comments.
tables=$(grep -hiE 'CREATE TABLE' "$MIGRATIONS_DIR"/*.sql \
  | grep -vE '^\s*--' \
  | grep -viE 'PARTITION OF' \
  | sed -E 's/.*CREATE TABLE +(IF NOT EXISTS +)?[`"]?([a-z_]+)[`"]? *\(.*/\2/I' \
  | grep -vxE 'goose_db_version' \
  | sort -u)

for t in $tables; do
  # Look for the table name wrapped in backticks anywhere in the doc.
  if ! grep -qF "\`$t\`" "$DB_DOC"; then
    echo "check-docs: table '$t' created by a migration is not mentioned in $DB_DOC" >&2
    exit_code=1
  fi
done

# ---------------------------------------------------------------- check 4
# Stale "deferred" wording in living docs only (design/ + CODEBUDDY.md).
# Advisory unless CHECK_DOCS_STRICT=1.
strict="${CHECK_DOCS_STRICT:-0}"
for doc in "${LIVING_DOCS[@]}"; do
  [ -f "$doc" ] || continue
  while IFS=: read -r line _; do
    [ -z "$line" ] && continue
    deferred_reviews=$((deferred_reviews + 1))
    if [ "$strict" = "1" ]; then
      echo "check-docs: stale 'deferred' wording at $doc:$line (strict mode)" >&2
      exit_code=1
    else
      echo "check-docs: review 'deferred' at $doc:$line" >&2
    fi
  done < <(grep -niE 'defer(red)?|已 ?defer' "$doc" || true)
done

# ---------------------------------------------------------------- check 5
# architecture.md Directory Layout vs. actual top-level directories.
# Extracts ONLY first-level directory names from the markdown tree:
# lines starting with ├── xxx/ or └── xxx/ (no │ prefix = first level).
# Whitelist: bin/ (build artifacts).
ARCH_DOC="design/architecture.md"
if [ -f "$ARCH_DOC" ]; then
  # Extract documented first-level dirs from tree blocks.
  # First-level lines start with ├── or └── (not │   ├──).
  doc_dirs=$(sed -n '/^```/,/^```/p' "$ARCH_DOC" \
    | grep -E '^(├|└)── [a-z0-9_-]+/' \
    | sed -E 's/^(├|└)── ([a-z0-9_-]+)\/.*/\2/' \
    | sort -u)

  # Actual top-level dirs (exclude hidden, .git, bin)
  actual_dirs=$(ls -d */ 2>/dev/null \
    | sed 's|/$||' \
    | grep -vE '^\.' \
    | grep -vxE 'bin' \
    | sort -u)

  # Whitelist bin/ in doc check
  doc_dirs_no_bin=$(echo "$doc_dirs" | grep -vxE 'bin' || true)

  if [ "$doc_dirs_no_bin" != "$actual_dirs" ]; then
    echo "check-docs: architecture.md Directory Layout mismatch (diff: < only-in-doc, > only-on-disk)" >&2
    diff <(printf '%s\n' "$doc_dirs_no_bin") <(printf '%s\n' "$actual_dirs") | sed 's/^/  /' >&2
    exit_code=1
  fi
fi

# ---------------------------------------------------------------- check 6
# architecture.md backtick paths (`path/`) must exist on disk.
# Only match full paths that contain at least one slash (e.g. `internal/authz/`),
# not bare directory names that happen to be part of a longer path.
if [ -f "$ARCH_DOC" ]; then
  while IFS= read -r path; do
    [ -z "$path" ] && continue
    # Skip paths with template vars or wildcards
    echo "$path" | grep -qE '[<>*?]' && continue
    # Skip bare directory names without slash (e.g. `authz/` from `internal/authz/`)
    # These are usually fragments of longer paths like `internal/authz/`.
    echo "$path" | grep -qE '^[^/]+/$' && continue
    # Strip trailing slash for test
    test_path="${path%/}"
    if [ ! -e "$test_path" ]; then
      echo "check-docs: architecture.md references non-existent path \`$path\`" >&2
      exit_code=1
    fi
  done < <(grep -oE '`[a-z0-9_/.-]+/`' "$ARCH_DOC" | sed 's/`//g' | sort -u)
fi

# ---------------------------------------------------------------- check 7
# ADR frontmatter Status: field must match the README table status.
# Extracts Status from each ADR file's frontmatter and diffs against README.
if [ -f "$ADR_INDEX" ]; then
  for adr_file in "$ADR_DIR"/[0-9][0-9][0-9][0-9]-*.md; do
    [ -f "$adr_file" ] || continue
    adr_num=$(basename "$adr_file" | sed -E 's/^([0-9]{4})-.*/\1/')
    # Extract Status from frontmatter: lines like `- Status: Proposed` or `- Status: Diagnostic (no impl)`
    file_status=$(sed -n '1,10p' "$adr_file" \
      | grep -E '^- Status:' \
      | sed -E 's/^- Status:[[:space:]]*//' \
      | sed 's/[[:space:]]*$//' \
      | head -1)
    # Extract status from README table: | [NNNN](...) | Title | Status |
    readme_status=$(grep -E "\[$adr_num\]" "$ADR_INDEX" \
      | sed -E 's/.*\|[^\|]+\|[^\|]+\|([^\|]+)\|.*/\1/' \
      | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    # Normalize: strip parenthetical comments for comparison
    # e.g. "Accepted (implemented step 4)" -> "Accepted"
    # e.g. "Resolved (superseded by ADR-0012)" -> "Resolved"
    file_status_base=$(echo "$file_status" | sed 's/ *(.*)$//')
    readme_status_base=$(echo "$readme_status" | sed 's/ *(.*)$//')
    if [ -n "$file_status_base" ] && [ -n "$readme_status_base" ] && [ "$file_status_base" != "$readme_status_base" ]; then
      echo "check-docs: ADR-$adr_num status mismatch: file says '$file_status', README says '$readme_status'" >&2
      exit_code=1
    fi
  done
fi

# ---------------------------------------------------------------- summary
hard_checks=6
if [ "$exit_code" -eq 0 ]; then
  if [ "$deferred_reviews" -gt 0 ]; then
    echo "check-docs: OK ($((hard_checks + 1)) checks, $deferred_reviews deferred-references to review)"
  else
    echo "check-docs: OK ($((hard_checks + 1)) checks)"
  fi
else
  echo "check-docs: FAIL — see above" >&2
fi

exit "$exit_code"
