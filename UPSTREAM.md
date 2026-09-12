# Upstream tracking

Chinese translation: [UPSTREAM.zh-CN.md](UPSTREAM.zh-CN.md).

Behavioral baseline: [guillaumemeyer/watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover)

| Field | Current value |
| --- | --- |
| Upstream branch | `main` |
| Last manual review | 2026-09-11 |
| Last reviewed release | `v0.7.0` |
| Automated check | `.github/workflows/upstream-sync.yml`, every 30 minutes and on demand |
| Local check | `make upstream-check` |

The workflow records the upstream `main` SHA in the `upstream_head:` line and
publishes a synchronization branch when it changes. Because repository policy
currently blocks Actions from creating pull requests, the workflow writes a
manual PR handoff to its run summary. A changed SHA does not mean that behavior
has been ported: review the upstream diff, update the mapping, and rerun the Go
and Python tests.

## Directory mapping

| Upstream module | Go implementation or entry point | Notes |
| --- | --- | --- |
| `service/scripts/text_unicode.py` | `internal/core/text.go` | Layer A classification, detection, and cleaning |
| `format_dispatch.py` | `internal/core/classify.go` | Extension and magic-byte routing |
| `image_meta.py` | `internal/core/image.go` | Image metadata and C2PA/JUMBF |
| `container_meta.py` | `internal/core/container.go`, `internal/core/pdf.go` | Documents, HTML, SVG, PDF, Markdown, LaTeX; optional tools |
| `av_meta.py` | `internal/core/av.go` | Audio/video containers and ID3/RIFF |
| `common.py` | `internal/core/io.go`, `process.go` | Limits, atomic writes, directory mirrors, reflink |
| `inspect_file.py`, `clean_file.py` | `cmd/aiwr`, `internal/core/process.go` | Unified CLI |
| `inspect_text.py`, `clean_text.py` | `aiwr inspect-text`, `clean-text` | stdin supported |
| `inspect_image.py`, `clean_image.py` | `aiwr inspect-image`, `clean-image` | Listed image formats |
| `rewrite_text.py`, `humanize_pass.py` | `cmd/aiwr/rewrite.go`, `internal/core/humanize.go` | Tactics, strategy, style, candidates, deterministic humanize |
| `server.py` | `internal/core/server.go`, `aiwr serve` | Native Go HTTP/OpenAPI/batch/auth and `/watermark` gateway |
| `text_watermark.py` | `internal/core/textwatermark.go` | Input validation and sidecar/local MarkLLM routing |
| `synthid_text_server.py` | `aiwr synthid-text-server` | Explicit upstream text SynthID adapter |
| `audit_dir.py` | `aiwr audit` | JSON/SARIF audit |
| `audit_website.py` | `aiwr audit-website` | Go-native sitemap/public URL audit |
| `clean_audio.py`, `clean_video.py` | `clean-audio`, `clean-video` plus ffmpeg hook | Metadata path is built in; heavy backends stay optional |
| `check_staged.py` | `aiwr check-staged` | Go-native batch check |
| `clean_staged.py` | `aiwr clean-staged` | Go-native in-place batch cleaning |
| `hook_written_file.py` | `aiwr hook-written-file` | Go-native PostToolUse stdin JSON |
| `detect_gumbel.py` | `internal/core/gumbel.go` | HMAC/EXP same-key verification |
| `score_stylometry.py` | `internal/core/stylometry.go` | Local heuristic score |
| `stealer/steal.py`, `scorer.py`, `tokens.py` | `internal/stealer`, `aiwr stealer` | Go tokenizer/scorer/query/build/detect |
| `stealer/download_prompts.py` | `internal/stealer`, `aiwr download-prompts` | Go-native pagination, checkpoint, and resume |
| Research scripts | `core.RunExternalCLI` | Explicit adapter directory; Python/runtime dependencies remain external |
| MarkLLM, SynthID, CtrlRegen, MarkDiffusion | Go argument/protocol adapters | Third-party algorithms and runtimes are not reimplemented or redistributed |

## Scope of the executable

`aiwr` aligns the upstream processing, detection, audit, service, hook, staged,
and stealer command entry points where the behavior is part of the native Go
product. The repository may retain Python scripts, skills, and benchmark files
as compatibility/reference assets, but their presence in the source tree does
not make them release contents or aiwr-maintained runtimes.

## Responsibility and release boundary

| Class | What belongs there | Release/maintenance rule |
| --- | --- | --- |
| A. Native aiwr | `cmd/aiwr`, `internal/core`, `internal/stealer`, Go tests and packages | Bundled in Go archives and native packages; maintained by aiwr |
| B. Optional adapter | `core.RunExternalCLI`, adapter argument/protocol wrappers, sidecar clients | Keep small and explicit; require the operator's third-party backend |
| C. Republished third-party runtime | Python/ML environments, model weights, backend Docker images | Not a formal aiwr release artifact; do not rebuild or publish it as aiwr |
| D. Development/CI | tests, CodeQL, Dependabot, GoReleaser, upstream-sync | Protect maintained code and packaging only |
| E. Compatibility/reference | `service/scripts`, skills, benchmark harnesses, upstream mapping | Track behavior deliberately; never let sync or Compose make it a product runtime |

The native Docker image, when used, runs the Go binary and contains only the
system tools needed by native format paths. It does not contain Python or ML
backends. Optional commands may use an explicitly supplied adapter directory
and externally managed environment.

## Porting rules

1. Read the upstream changelog, coverage matrix, and affected source before
   writing a Go regression fixture.
2. Preserve upstream safety refusals: unknown formats must not become text
   silently, limits must not permit unbounded memory, and originals are not
   overwritten by default.
3. Connect optional Python/GPU/research dependencies only through an explicit
   adapter or sidecar; do not mix their code, weights, or licenses into the
   MIT core or its formal runtime images.
4. Record behavior as aligned, degraded, or pending in synchronization PRs.
5. Before release run `go test ./...`, `go vet ./...`, `make smoke`, and the
   directory/reflink/HTTP smoke checks. Do not publish third-party backend
   images as aiwr artifacts.

upstream_head: 81d808d5d71bb22a02b1bdc3df293a2d93422778
