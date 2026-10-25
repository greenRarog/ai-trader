#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ ! -x "$project_dir/bin/ai-trader" || ! -f "$project_dir/bin/ca-certificates.crt" ]]; then
  echo 'Build the local binary first: bash scripts/build-local.sh' >&2
  exit 1
fi
cd -- "$project_dir"
# The Go loader reads .env without executing shell expressions.
# Prefer the mounted Unix socket; TCP routes are explicit alternatives.
case "${AI_TRADER_DB_ROUTE:-socket}" in
  socket)
    if [[ ! -S "$project_dir/.runtime/postgres/.s.PGSQL.5432" ]]; then
      echo 'PostgreSQL socket is missing. Run: docker compose up -d --wait postgres' >&2
      exit 1
    fi
    export AI_TRADER_LOCAL=0
    export POSTGRES_HOST="$project_dir/.runtime/postgres"
    ;;
  published)
    export AI_TRADER_LOCAL=1
    ;;
  bridge)
    if [[ "$(uname -s)" != Linux ]]; then
      echo 'Direct container IP routing is supported only on Linux Docker Engine.' >&2
      exit 1
    fi
    container_id="$(docker compose ps -q postgres)"
    if [[ -z "$container_id" ]]; then
      echo 'Start PostgreSQL first: docker compose up -d --wait postgres' >&2
      exit 1
    fi
    container_ips="$(docker inspect --format '{{range .NetworkSettings.Networks}}{{println .IPAddress}}{{end}}' "$container_id")"
    read -r container_ip <<< "$container_ips"
    if [[ ! "$container_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo 'Cannot determine the PostgreSQL container IPv4 address.' >&2
      exit 1
    fi
    export AI_TRADER_LOCAL=0
    export POSTGRES_HOST="$container_ip:5432"
    ;;
  *)
    echo 'AI_TRADER_DB_ROUTE must be socket, published or bridge.' >&2
    exit 1
    ;;
esac
export SSL_CERT_FILE="${SSL_CERT_FILE:-$project_dir/bin/ca-certificates.crt}"
exec "$project_dir/bin/ai-trader" "$@"
