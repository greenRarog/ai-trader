#!/usr/bin/env bash
set -euo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# != 1 || ! -f "$1" ]]; then echo 'Usage: bash scripts/check-backup.sh /path/to/backup.dump' >&2; exit 1; fi
backup_path="$(realpath -- "$1")"
backup_dir="$(dirname -- "$backup_path")"
backup_name="$(basename -- "$backup_path")"
if [[ ! -f "$backup_path.sha256" ]]; then echo 'Checksum sidecar is missing.' >&2; exit 1; fi
# Hash the selected file directly; do not trust paths embedded in a sidecar.
expected="$(awk 'NR==1 {print $1}' "$backup_path.sha256")"
actual="$(sha256sum -- "$backup_path")"
actual="${actual%% *}"
[[ "$expected" =~ ^[a-f0-9]{64}$ && "$actual" == "$expected" ]] || { echo 'Backup checksum mismatch.' >&2; exit 1; }
cd -- "$project_dir"
mkdir -p .state
exec 9>.state/restore-check.lock
flock -n 9 || { echo 'A restore check is already running.' >&2; exit 1; }
# This dedicated profile has no production volumes or published ports.
cleanup() { docker compose --profile restore-check rm --stop --force postgres-restorecheck >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker compose --profile restore-check up -d --wait postgres-restorecheck
docker compose exec -T postgres-restorecheck sh -eu -c '
 export PGPASSWORD="$POSTGRES_PASSWORD"
 exec pg_restore --host=/var/run/postgresql --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --exit-on-error --no-owner --no-privileges
' < "$backup_path"
docker compose exec -T postgres-restorecheck sh -eu -c '
 export PGPASSWORD="$POSTGRES_PASSWORD"
 exec psql --host=/var/run/postgresql --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --set=ON_ERROR_STOP=1
' <<'SQL'
BEGIN;
UPDATE trading_accounts SET paused=true,pause_reason='isolated restore check';
UPDATE notification_outbox SET state='rejected',error='isolated restore check' WHERE state IN ('queued','sending');
SELECT 'schema_version',max(version) FROM schema_migrations;
SELECT 'analysis_runs',count(*) FROM analysis_runs;
SELECT 'order_intents',count(*) FROM order_intents;
SELECT 'cashflows',count(*) FROM cashflows;
COMMIT;
SQL
printf 'Restore check OK: %s (isolated temporary PostgreSQL)\n' "$backup_name"
