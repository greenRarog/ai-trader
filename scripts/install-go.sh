#!/usr/bin/env bash
set -euo pipefail
# Install inside this project; no sudo or changes to shell profiles.
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
version=1.27.3
checksum=65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo 'This installer supports Linux x86_64. See https://go.dev/dl/ for other platforms.' >&2
  exit 1
fi
if [[ -e "$project_dir/.tools/go" ]]; then
  echo 'Local Go directory already exists; leaving it unchanged.' >&2
  exit 1
fi
mkdir -p "$project_dir/.tools"
archive="$(mktemp)"
trap 'rm -f "$archive"' EXIT
curl --fail --location --connect-timeout 10 --max-time 180 \
  "https://go.dev/dl/go${version}.linux-amd64.tar.gz" --output "$archive"
printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --status
tar -xzf "$archive" -C "$project_dir/.tools"
"$project_dir/.tools/go/bin/go" version
printf 'Run: export PATH="%s/.tools/go/bin:$PATH"\n' "$project_dir"
