# aiwr packaging

aiwr uses nFPM as the single native-package engine. One configuration produces
Debian packages, RPM packages, Alpine APKs, and Arch Linux packages. This keeps
the file list, permissions, metadata, and documentation identical across
distributions.

## Local build

Install nFPM once:

    go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest

Then build one format or all four:

    make package-deb
    make package-rpm
    make package-apk
    make package-arch
    make package-all

The packages are written to dist. The build is offline after Go modules and
nFPM are installed. The Go binary is built with CGO disabled, so the package
does not add a runtime library dependency. Optional features still use tools
that the user explicitly installs, such as ffmpeg, qpdf, Ghostscript, or
ExifTool.

Useful overrides:

    VERSION=0.1.0 TARGET_GOARCH=amd64 make package-deb
    VERSION=0.1.0 TARGET_GOARCH=arm64 make package-all
    NFPM=/path/to/nfpm bash scripts/package-nfpm.sh all

The script accepts deb, rpm, apk, archlinux, or all. TARGET_GOARCH follows Go
names: amd64, arm64, arm, or 386. nFPM translates the architecture to each
package manager's naming convention.

## Install locally built packages

Use the package manager native to the target distribution and select the exact
artifact produced in dist:

    sudo apt install ./dist/aiwr_0.1.0_amd64.deb
    sudo dnf install ./dist/aiwr_0.1.0_amd64.rpm
    sudo pacman -U ./dist/aiwr_0.1.0_amd64.pkg.tar.zst
    sudo apk add --allow-untrusted ./dist/aiwr_0.1.0_amd64.apk

The APK command needs a trusted repository or an explicit local-package policy
on systems that reject unsigned local APKs. For production repositories, sign
the package and publish the repository key according to the distribution's
policy.

## GitHub releases

Pushing a tag such as v0.1.0 triggers .github/workflows/release.yml. The
GoReleaser configuration:

- builds static Linux, macOS, and Windows archives for amd64, arm64, and armv7;
- invokes nFPM for deb, rpm, and apk release artifacts;
- includes the Arch Linux packager configuration in the same nFPM pipeline;
- attaches checksums.txt to the release.

GoReleaser runs nFPM internally, so maintainers do not need separate packaging
logic for release builds. SOURCE_DATE_EPOCH is honored by nFPM and should be
set by the release environment for reproducible timestamps.

The macOS and Windows release path is an archive rather than a native package:
extract it, put aiwr on PATH, and run aiwr version. A Homebrew tap and winget
manifest are intentionally tracked as a later release-channel task in the
roadmap; the signed release archives remain available in the meantime.

## Package contents

Every package installs:

- usr/bin/aiwr;
- the README, user guide, AI agent guide, roadmap, and upstream mapping;
- the MIT license and aiwr manual page;
- the optional clean strategy example under usr/share/aiwr.

No package silently downloads Python environments, model weights, external
checkouts, or optional system tools.
