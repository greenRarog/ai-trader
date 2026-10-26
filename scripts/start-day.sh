#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$project_dir"
# start is idempotent: an already running trader/model is not interrupted.
# The trader unit's ExecStartPre starts PostgreSQL and waits for readiness.
systemctl --user start ai-trader.service ai-trader-backup.timer ai-trader-report.timer
bash scripts/trader.sh db ping
bash scripts/trader.sh trading status
systemctl --user is-active ai-trader.service ai-trader-backup.timer ai-trader-report.timer
systemctl --user list-timers ai-trader-backup.timer ai-trader-report.timer --no-pager
