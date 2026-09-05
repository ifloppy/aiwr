#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

bash -n "$repo_root/scripts/package-nfpm.sh"

test -s "$repo_root/.goreleaser.yaml"
test -s "$repo_root/packaging/nfpm.yaml"
test -s "$repo_root/packaging/aiwr.1"
test -s "$repo_root/packaging/README.md"

grep -q '^name: aiwr$' "$repo_root/packaging/nfpm.yaml"
grep -q '^[[:space:]]*formats:' "$repo_root/.goreleaser.yaml"
printf '%s\n' 'packaging metadata ok'
