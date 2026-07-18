#!/usr/bin/env bash
# Architecture dependency checks enforcing design/architecture.md's rules using
# `go list -deps` (no external tooling). Currently-empty packages are simply
# skipped, so these act as preventive gates that engage automatically once the
# packages gain code.
#
# Rules enforced:
#   1. Data plane (proxy) and management plane (admin) must not import each other.
#   2. L0 pkg/ must not import anything under internal/.
#   3. Provider adapters must not import sibling adapters (shared/ is exempt).
#   4. Plugins must not import sibling plugins.
set -euo pipefail

cd "$(dirname "$0")/.."
GO="${GO:-go}"
MOD=voxeltoad
fail=0

violation() {
	echo "VIOLATION: $1"
	fail=1
}

deps() {
	# Print module-internal dependency import paths for a package pattern.
	$GO list -deps "$1" 2>/dev/null || true
}

# Rule 1: proxy <-> admin isolation.
if deps ./internal/proxy/... | grep -q "$MOD/internal/admin"; then
	violation "internal/proxy imports internal/admin"
fi
if deps ./internal/admin/... | grep -q "$MOD/internal/proxy"; then
	violation "internal/admin imports internal/proxy"
fi

# Rule 2: pkg/ (L0) must not depend on internal/.
if deps ./pkg/... | grep -q "$MOD/internal/"; then
	violation "pkg/ imports internal/ (L0 must stay free of internal)"
fi

# Rules 3 & 4: sibling packages within a group must not import one another.
# The "shared" sibling is the sanctioned location for common helpers and is
# therefore exempt as an import target.
check_siblings() {
	local group="$1"
	local pkgs
	pkgs=$($GO list "./$group/..." 2>/dev/null || true)
	[ -z "$pkgs" ] && return 0

	local p self d other
	for p in $pkgs; do
		[ "$p" = "$MOD/$group" ] && continue
		self=${p#"$MOD/$group/"}
		self=${self%%/*}
		d=$(deps "$p")
		for other in $(echo "$pkgs" | sed -E "s|^$MOD/$group/||;s|/.*$||" | sort -u); do
			[ "$other" = "$self" ] && continue
			[ "$other" = "shared" ] && continue
			[ "$other" = "$MOD/$group" ] && continue
			if echo "$d" | grep -qE "^$MOD/$group/$other(/|$)"; then
				violation "$group/$self imports sibling $group/$other"
			fi
		done
	done
}

check_siblings internal/adapter
check_siblings internal/plugin

if [ "$fail" -ne 0 ]; then
	echo "arch-check: FAILED"
	exit 1
fi
echo "arch-check: OK"
