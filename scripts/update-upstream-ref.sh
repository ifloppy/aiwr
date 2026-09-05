#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ! "$1" =~ ^[0-9a-fA-F]{40}$ ]]; then
	printf 'usage: %s FULL_40_CHAR_SHA\n' "$0" >&2
	exit 2
fi
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="$repo_root/UPSTREAM.md"
tmp="$file.tmp"
sed "s/^upstream_head: .*/upstream_head: $1/" "$file" > "$tmp"
mv "$tmp" "$file"
