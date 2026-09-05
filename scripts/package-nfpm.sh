#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
go_bin=${GO:-go}
nfpm_bin=${NFPM:-nfpm}
format=${1:-all}
version=${VERSION:-$(awk -F'"' '/^const Version = / {print $2; exit}' "$repo_root/internal/core/server.go")}
target_goarch=${TARGET_GOARCH:-$("$go_bin" env GOARCH)}

if ! command -v "$nfpm_bin" >/dev/null 2>&1; then
    echo "error: nFPM is required; install with:" >&2
    echo "  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest" >&2
    exit 2
fi
if [[ ! "$version" =~ ^[0-9A-Za-z.+:~-]+$ ]]; then
    echo "error: invalid VERSION: $version" >&2
    exit 2
fi

case "$target_goarch" in
    amd64|x86_64) nfpm_arch=amd64 ;;
    arm64|aarch64) nfpm_arch=arm64 ;;
    arm) nfpm_arch=arm7 ;;
    386|i386|i686) nfpm_arch=386 ;;
    *)
        echo "error: unsupported TARGET_GOARCH for nFPM: $target_goarch" >&2
        exit 2
        ;;
esac

case "$format" in
    all) formats="deb rpm apk archlinux" ;;
    deb|rpm|apk|archlinux) formats="$format" ;;
    *)
        echo "usage: $0 [all|deb|rpm|apk|archlinux]" >&2
        exit 2
        ;;
esac

if [[ -z ${SOURCE_DATE_EPOCH:-} ]]; then
    SOURCE_DATE_EPOCH="$(git -C "$repo_root" log -1 --format=%ct 2>/dev/null || date +%s)"
fi
export VERSION="$version" NFPM_ARCH="$nfpm_arch" SOURCE_DATE_EPOCH

mkdir -p "$repo_root/dist/nfpm"
GOOS=linux GOARCH="$target_goarch" CGO_ENABLED=0 \
    "$go_bin" build -trimpath -ldflags="-s -w" \
    -o "$repo_root/dist/nfpm/aiwr" "$repo_root/cmd/aiwr"

cd "$repo_root"
for packager in $formats; do
    "$nfpm_bin" package \
        --config packaging/nfpm.yaml \
        --packager "$packager" \
        --target "$repo_root/dist"
done
