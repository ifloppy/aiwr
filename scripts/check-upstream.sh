#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tracked="$(sed -n 's/^upstream_head: *//p' "$repo_root/UPSTREAM.md" | head -n1)"
remote="$(git ls-remote https://github.com/guillaumemeyer/watermarks-remover.git refs/heads/main | awk '{print $1}')"
if [[ -z "$remote" ]]; then
	printf '%s\n' 'unable to read upstream main' >&2
	exit 2
fi
	printf 'tracked: %s\nremote:  %s\n' "${tracked:-UNKNOWN}" "$remote"
if [[ "$tracked" == "$remote" ]]; then
	printf '%s\n' 'upstream is up to date'
	exit 0
fi
	printf '%s\n' 'upstream changed; review and run the sync workflow' >&2
exit 1
