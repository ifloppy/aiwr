# aiwr

`aiwr` is a Go reimplementation and compatibility mirror of
[guillaumemeyer/watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover).
It inspects and removes detectable Unicode carriers and file metadata from
content that you own or are authorized to process. It is released under the
MIT License; see [LICENSE](LICENSE).

中文文档：[README.zh-CN.md](README.zh-CN.md) · [中文用户手册](docs/USER_GUIDE.zh-CN.md)

## What it covers

- Layer A text cleanup: zero-width and space-like characters, bidi controls,
  selected homoglyphs, NFKC normalization, and emoji glue options.
- File metadata cleanup for PNG, JPEG, WebP, AVIF/HEIC, BMP, GIF, TIFF,
  SVG, PDF, DOCX/XLSX/PPTX, ODT, EPUB, HTML, Markdown, MP4/MOV/M4A/M4V,
  WAV, MP3, and FLAC.
- Inspection, detection, directory mirroring, JSON, SARIF, stdin, staged-file
  checks, PostToolUse hooks, an HTTP API, website auditing, and the stealer
  research workflow.
- Optional Layer B rewriting through a local Ollama or explicitly authorized
  OpenAI-compatible endpoint. Heavy model and GPU dependencies are not
  downloaded or embedded in the default build.

The project preserves the upstream repository layout and compatibility assets,
including `service/scripts`, `skills`, the Claude plugin, installer, hooks,
pre-commit configuration, Docker files, and benchmark entry points. See
[UPSTREAM.md](UPSTREAM.md) for the mapping and synchronization rules.

## Install and verify

Go 1.23 or newer is required for a source build. The core module only needs
the Unicode support dependency declared in `go.mod`.

```bash
go build -trimpath -o aiwr ./cmd/aiwr
go install github.com/iruanp/aiwr/cmd/aiwr@latest

go test ./...
go vet ./...
./aiwr version
```

Distribution packages and offline build recipes are documented in
[packaging/README.md](packaging/README.md).

## Quick start

```bash
# Inspect without modifying the source.
aiwr inspect draft.txt

# Write draft.cleaned.txt beside the source.
aiwr clean draft.txt

# Mirror a directory to drafts.cleaned/.
aiwr clean ./drafts

# Replace a file in place after creating draft.txt.bak.
aiwr clean draft.txt --in-place

# Clean text from stdin.
printf 'hello\u200bworld\n' | aiwr clean-text -
```

The default destination is new: `NAME.cleaned.EXT` for a file and
`DIRECTORY.cleaned` for a directory. Unknown files are not silently treated
as text. Use `--as` or `--force-text` only when that interpretation is
intentional.

## Commands

| Command | Purpose |
| --- | --- |
| `clean`, `clean-file` | Inspect the type and clean a file or directory |
| `inspect`, `inspect-file` | Report metadata, findings, and residual risk |
| `detect` | Produce a detection summary |
| `audit`, `audit-dir` | Scan a file or directory; supports JSON and SARIF |
| `clean-text`, `inspect-text` | Force the text pipeline; supports stdin |
| `clean-image`, `inspect-image` | Force the image pipeline |
| `clean-audio`, `clean-video` | Clean media containers; optional lossy processing |
| `score-stylometry` | Run the local heuristic text-style score |
| `detect-gumbel` | Recheck a same-key HMAC/EXP Gumbel watermark |
| `rewrite-text` | Print a Layer B prompt or use an explicit provider |
| `audit-website` | Audit public URLs from a same-origin sitemap |
| `check-staged`, `clean-staged` | Check or clean repository files |
| `hook-written-file` | Check or clean a file named by a PostToolUse payload |
| `stealer query|build|detect` | Run the model-free black-box research workflow |
| `download-prompts` | Download a resumable prompt corpus |
| `serve` | Start the HTTP service on `127.0.0.1:8765` by default |

Optional research commands such as `score-synthid`, `markdiffusion`,
`clean-ctrlregen`, and `detect-text-watermark` delegate to the bundled
upstream scripts when available. They still require the relevant external
checkout, Python packages, model weights, or sidecar. Use
`--upstream-scripts PATH` or `AIWR_UPSTREAM_SCRIPTS` to select another script
directory.

## CLI language

English is the default. Human-readable help, status, progress, and diagnostic
messages use Chinese when a Chinese locale is detected. The precedence is:

1. `AIWR_LANG` or `AIWR_LANGUAGE` (explicit override);
2. `LC_ALL`, `LC_MESSAGES`, `LANGUAGE`, then `LANG`.

Values beginning with `zh` select Chinese. Unsupported, empty, or malformed
values fall back to English. Machine-readable JSON and SARIF output remain
language-neutral and stable. For example:

```bash
AIWR_LANG=zh-CN aiwr --help
LANG=en_US.UTF-8 aiwr --help
```

The help forwarded by an external upstream Python command remains the
upstream command's own output; the Go wrapper and its fallback help follow the
selected CLI language.

## Safety and authorization

Only process files, directories, and websites that you own or are authorized
to handle. Inspect before cleaning, write to a new destination by default,
and inspect the output again:

```bash
aiwr inspect --json input.md > before.json || test $? -eq 1
aiwr clean --json --output input.cleaned.md input.md
aiwr inspect --json input.cleaned.md > after.json || test $? -eq 1
```

`--in-place`, `--force-text`, `--nfkc`, aggressive homoglyph handling,
`--strip-bidi`, `--strip-emoji-glue`, audio remixing, pixel removal, remote
model calls, and website/prompt downloads require an intentional choice.
Read [docs/AI_AGENT_GUIDE.md](docs/AI_AGENT_GUIDE.md) before operating aiwr
on a user's behalf. The project does not promise to remove private, keyed, or
pixel-domain watermarks, make a detector fail, or prove human authorship.

## HTTP API

```bash
aiwr serve
curl http://127.0.0.1:8765/health
```

The service exposes `/health`, `/capabilities`, `/openapi.json`, `/inspect`,
`/detect`, `/clean`, `/watermark`, and their batch counterparts. Requests use
base64 file content; the text watermark endpoint also accepts `text`. Set
`WATERMARKS_SERVER_API_KEY` (or `WATERMARKS_API_KEY`) before exposing the
service beyond loopback. See [docs/USER_GUIDE.md](docs/USER_GUIDE.md) for the
request shape, limits, authentication, and optional sidecars.

## Development and upstream tracking

```bash
make format
make test
make vet
make smoke
make upstream-check
```

The upstream `main` commit is tracked in [UPSTREAM.md](UPSTREAM.md), and the
scheduled workflow checks for changes. Review the upstream diff and run the
full test suite before accepting a sync.

## License and limitations

This project and the upstream compatibility code are MIT-licensed. Optional
models, tools, and backends remain subject to their own licenses. Metadata
cleaning is best-effort: provenance, pixel, waveform, unknown private, and
secret-key signals may remain. Follow applicable law, platform policy, and
attribution or disclosure requirements.
