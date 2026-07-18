#!/usr/bin/env bash
# Start local development dependencies as containers. The gateway's only
# stateful dependency is PostgreSQL; rate limiting and caching default to
# in-memory implementations (Redis is an opt-in scaling concern, not started
# here). Requires docker. Idempotent; stop with: scripts/dev-deps.sh down
#
# Note: for unit/integration tests, prefer embedded-postgres (no Docker) — see
# design/unit-test.md. This script is for running the full gateway locally.
set -euo pipefail

PG_NAME=voxeltoad-postgres

up() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "docker not found; please install Docker to use this script." >&2
    exit 1
  fi

  if docker ps -a --format '{{.Names}}' | grep -qx "$PG_NAME"; then
    echo "[$PG_NAME] already exists; starting if stopped"
    docker start "$PG_NAME" >/dev/null
  else
    echo "[$PG_NAME] creating"
    docker run -d --name "$PG_NAME" \
      -e POSTGRES_PASSWORD=postgres \
      -e POSTGRES_DB=voxeltoad \
      -p 5432:5432 postgres:16 >/dev/null
  fi

  echo "Dev dependencies are up:"
  echo "  PostgreSQL  localhost:5432  (user=postgres pass=postgres db=voxeltoad)"
}

down() {
  if docker ps -a --format '{{.Names}}' | grep -qx "$PG_NAME"; then
    echo "[$PG_NAME] removing"
    docker rm -f "$PG_NAME" >/dev/null
  fi
  echo "Dev dependencies removed."
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  *) echo "usage: $0 [up|down]" >&2; exit 1 ;;
esac
