#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ! "$1" =~ ^[0-9a-fA-F]{40}$ ]]; then
	printf 'usage: %s FULL_40_CHAR_SHA\n' "$0" >&2
	exit 2
fi
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for file in "$repo_root/UPSTREAM.md" "$repo_root/UPSTREAM.zh-CN.md"; do
    [[ -f "$file" ]] || continue
    tmp="$file.tmp"
    sed "s/\(upstream_head: \)[0-9a-fA-F]\{40\}/\1$1/" "$file" > "$tmp"
    mv "$tmp" "$file"
done
