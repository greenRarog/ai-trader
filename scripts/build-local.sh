#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$(uname -s)" != Linux ]]; then
  echo 'The local launcher currently targets Linux.' >&2
  exit 1
fi
mkdir -p "$project_dir/bin"
docker build --target local-artifact --output "type=local,dest=$project_dir/bin" "$project_dir"
chmod u+x "$project_dir/bin/ai-trader"
echo 'Local binary built. Run: bash scripts/trader.sh help'
