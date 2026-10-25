#!/usr/bin/env bash
set -euo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$project_dir"
verify=false
if [[ "${1:-}" == --verify ]]; then verify=true; shift; fi
if [[ $# != 0 ]]; then echo 'Usage: bash scripts/backup-db.sh [--verify]' >&2; exit 1; fi
# This backup targets the Compose database. Refuse an explicit external DSN
# rather than silently backing up a different database than the application.
python3 - "$project_dir/.env" <<'PYCONF'
import os,pathlib,sys
settings={}
path=pathlib.Path(sys.argv[1])
if path.exists():
    for line in path.read_text().splitlines():
        key,sep,value=line.partition('=')
        if sep:settings[key.strip()]=value.strip().strip('"\'')
if os.environ.get('DATABASE_URL',settings.get('DATABASE_URL','')):
    sys.exit('DATABASE_URL is explicitly set. Back up that database separately; this script targets Compose PostgreSQL.')
PYCONF
backup_dir="${BACKUP_DIR:-$project_dir/.backups}"
backup_keep="${BACKUP_KEEP:-14}"
if [[ ! "$backup_keep" =~ ^[0-9]+$ ]] || ((10#$backup_keep < 1 || 10#$backup_keep > 365)); then
  echo 'BACKUP_KEEP must be 1..365.' >&2; exit 1
fi
mkdir -p -- "$backup_dir"
backup_dir="$(cd -- "$backup_dir" && pwd)"
exec 9>"$backup_dir/.backup.lock"
flock -n 9 || { echo 'A backup is already running.' >&2; exit 1; }
temporary="$(mktemp "$backup_dir/.backup.XXXXXX")"
trap 'rm -f -- "$temporary"' EXIT
# Credentials remain inside the container environment, never in process args.
docker compose exec -T postgres sh -eu -c '
  export PGPASSWORD="$POSTGRES_PASSWORD"
  exec pg_dump --host=/var/run/postgresql --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --format=custom --no-owner --no-privileges
' > "$temporary"
[[ -s "$temporary" ]] || { echo 'Empty database dump.' >&2; exit 1; }
docker compose exec -T postgres pg_restore --list < "$temporary" > /dev/null
backup_name="ai-trader-$(date -u +%Y%m%dT%H%M%S%NZ).dump"
backup_path="$backup_dir/$backup_name"
mv -- "$temporary" "$backup_path"
(cd -- "$backup_dir" && sha256sum -- "$backup_name" > "$backup_name.sha256")
if "$verify"; then bash "$project_dir/scripts/check-backup.sh" "$backup_path"; fi
# Prune only our timestamped dumps after successful creation/verification.
python3 - "$backup_dir" "$backup_keep" <<'PY'
import pathlib,re,sys
root=pathlib.Path(sys.argv[1]);keep=int(sys.argv[2])
files=sorted((p for p in root.iterdir() if re.fullmatch(r'ai-trader-\d{8}T\d{15}Z\.dump',p.name) and p.is_file() and not p.is_symlink()),key=lambda p:p.name,reverse=True)
for p in files[keep:]:
    p.unlink();p.with_name(p.name+'.sha256').unlink(missing_ok=True)
PY
printf 'Backup saved: %s\n' "$backup_path"
