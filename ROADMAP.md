# aiwr Roadmap

Chinese translation: [ROADMAP.zh-CN.md](ROADMAP.zh-CN.md).

The roadmap follows `guillaumemeyer/watermarks-remover` while keeping Go-native
implementation and safety improvements separate from compatibility work.
Upstream changes arrive through reviewed synchronization PRs, not silently
downloaded runtime code.

## Completed

- [x] Unified `clean`, `inspect`, `detect`, `audit`, and forced-format aliases.
- [x] Layer A Unicode inspection, statistics, NFKC, spaces, homoglyphs, bidi,
  and emoji options.
- [x] Image, document/container, audio/video metadata paths and PDF fallbacks.
- [x] stdin, JSON, SARIF, unknown-format refusal, size limits, and binary guard.
- [x] Atomic writes, `.bak` preservation, symlink boundaries, directory mirror
  output, and Linux FICLONE `reflink auto|always|never`.
- [x] HTTP API, OpenAPI, batch limits, Bearer authentication, and Layer B
  strategy routing.
- [x] Layer B prompt/Ollama/OpenAI-compatible adapters, local stylometry,
  same-key Gumbel verification, MarkLLM routing, and text watermark gateway.
- [x] Go-native website audit, staged-file commands, PostToolUse hook, stealer
  workflow, resumable prompt downloader, CI, packaging, and upstream sync.
- [x] Bilingual English-default documentation and locale-aware CLI output.

## Completed compatibility work

- [x] Configurable `/clean` strategy with explicit Layer-A-only behavior when a
  provider is absent.
- [x] `rewrite-text` tactics, rewrite level, style, back-translation, humanize,
  chunk strategy, candidate evaluation, and explicit external backends.
- [x] SynthID HTTP scoring, CtrlRegen, MarkDiffusion, TrustMark per-frame video
  adapter, and configurable audio remix hooks.
- [x] EPUB encrypted parts, OOXML metadata/relationships/content types, and
  truncated-media regression fixtures.

## Remaining engineering work

- [ ] Fuzz PNG/JPEG/WebP/TIFF/ISOBMFF/RIFF/ZIP truncation, compression bombs,
  and integer-overflow boundaries.
- [ ] Windows/macOS reflink capability detection and cross-platform
  permissions/symlink tests.
- [ ] Use `--jobs` with a real worker pool while preserving directory order and
  error aggregation.
- [ ] Versioned JSON Schema/OpenAPI clients and a stable semver policy.
- [ ] Automatic coverage diffs, compatibility reports, and migration notes for
  each upstream release.
- [ ] Signed releases, SBOM, and official Homebrew/winget distribution.

## Explicit boundaries

Research commands retain their upstream entry points as optional adapters, but
source checkouts, Python packages, model weights, and sidecars remain explicit
operator dependencies. A source checkout may provide adapter scripts under
`service/scripts`; installed binaries require an explicit adapter directory via
`--upstream-scripts` or `AIWR_UPSTREAM_SCRIPTS`. aiwr does not redistribute the
third-party ML runtimes. Website audit, staged commands, hooks, stealer
scoring, and prompt download do not require Python.

## Non-goals

- No promise to remove unknown private or secret-key watermarks.
- No promise that an official vendor detector will fail.
- No default model download, third-party code execution, network upload, or
  modification of the original file.
- No claim that cleaning proves human authorship or removes attribution and
  disclosure obligations.
