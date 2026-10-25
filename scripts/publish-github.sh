#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$project_dir"
if [[ $# != 0 ]]; then echo 'Usage: bash scripts/publish-github.sh' >&2; exit 1; fi
repo='GreenRarog/ai-trader'
command -v gh >/dev/null || { echo 'GitHub CLI is required: install gh, then run gh auth login. Existing SSH access alone cannot create a repository.' >&2; exit 1; }
gh auth status >/dev/null
login="$(gh api user --jq .login)"
if [[ "${login,,}" != greenrarog ]]; then echo 'Active GitHub API account must be GreenRarog.' >&2; exit 1; fi
[[ -z "$(git status --porcelain)" ]] || { echo 'Commit the reviewed project changes before publishing.' >&2; exit 1; }
git rev-parse --verify HEAD >/dev/null
python3 scripts/check-secrets.py
origin="$(git remote get-url origin 2>/dev/null || true)"
if [[ -n "$origin" && "$origin" != "git@github.com:$repo.git" && "$origin" != "https://github.com/$repo.git" && "$origin" != "https://github.com/$repo" ]]; then
 echo 'origin points to another repository; refusing to overwrite it.' >&2; exit 1
fi
# An API read must succeed before treating a repository as existing. Only a
# successful creation grants the new private target; other failures stop here.
if metadata="$(gh api "repos/$repo" --jq '[.full_name,.private] | @tsv' 2>/dev/null)"; then
 IFS=$'\t' read -r existing visibility <<< "$metadata"
 if [[ "${existing,,}" != greenrarog/ai-trader || "$visibility" != true ]]; then
  echo 'Target already exists with unexpected owner/name or is not private; refusing to publish.' >&2; exit 1
 fi
else
 gh repo create "$repo" --private --description 'Go CLI AI trading research in T-Invest Sandbox: LLM decisions, risk checks and durable audit.'
fi
if [[ -z "$origin" ]]; then git remote add origin "git@github.com:$repo.git"; fi
# Ordinary push only: never overwrite existing remote history.
git push --set-upstream origin main
printf 'Published: https://github.com/%s\n' "$repo"
