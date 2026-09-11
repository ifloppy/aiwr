# aiwr User Guide

Chinese translation: [USER_GUIDE.zh-CN.md](USER_GUIDE.zh-CN.md). If an AI
agent operates aiwr on a user's behalf, also read
[AI_AGENT_GUIDE.md](AI_AGENT_GUIDE.md).

## 1. Inspect, clean, inspect again

The safe default is to inspect first and write to a new destination:

```bash
aiwr inspect input.md --json > before.json || test $? -eq 1
aiwr clean input.md --output input.cleaned.md --json > clean.json
aiwr inspect input.cleaned.md --json > after.json || test $? -eq 1
```

Exit code 1 means a finding or residual was reported, not that the program
crashed. Exit code 2 means invalid arguments, refusal, or an unsupported single
file. Exit code 3 means a batch contains failures. Prefer JSON for decisions;
human output is intended for interactive review.

`clean` writes `NAME.cleaned.EXT` beside a file and `DIRECTORY.cleaned` beside
a directory. Multiple files require `--output-dir`. Directory output preserves
relative names, permissions, and symbolic links. It must not equal the input or
be inside it. Unknown files are copied unchanged in a directory by default;
`--skip-unknown` omits them.

Use `--in-place` only for an explicit original-file change. It creates
`FILE.bak` without overwriting an existing backup. Never use it for a
directory.

## 2. Language selection

English is the default CLI language. Chinese is selected by a value beginning
with `zh` in `AIWR_LANG`/`AIWR_LANGUAGE`, `LC_ALL`, `LC_MESSAGES`,
`LANGUAGE`, or `LANG`, in that order. Unsupported or missing values fall back
to English:

```bash
AIWR_LANG=zh-CN aiwr inspect --help
LANG=en_US.UTF-8 aiwr inspect --help
```

Help, status, progress, and diagnostics are localized. JSON and SARIF are not
translated so their fields remain stable. Help printed by an external upstream
Python adapter remains the upstream command's own output.

## 3. Text Layer A

```bash
aiwr inspect-text article.txt
aiwr clean-text article.txt --nfkc --aggressive-homoglyphs --stats
cat article.txt | aiwr clean-text --strip-bidi -
```

The default removes deterministic invisible carriers and normalizes common
space-like characters. `--no-normalize-spaces` preserves them. The options
`--nfkc`, `--aggressive-homoglyphs`, `--strip-bidi`, and
`--strip-emoji-glue` can alter valid multilingual text, layout, or emoji
sequences; review a diff before publishing.

Text `sample_offsets` are decoded rune offsets, not byte columns. Invalid
UTF-8 bytes are preserved. Text-only commands reject binary-looking input;
use `--force-text` only when raw-byte interpretation is deliberate.

## 4. Files, containers, and media

```bash
aiwr inspect-image shot.png
aiwr clean-image shot.png --output shot.cleaned.png
aiwr clean notes.docx
aiwr clean slides.pptx
aiwr clean book.epub
aiwr clean clip.mp4
```

The built-in paths cover common PNG/JPEG/WebP/AVIF/HEIC/BMP/GIF/TIFF image
metadata, SVG/PDF/OOXML/ODT/EPUB/HTML/Markdown/LaTeX containers, and MP4/MOV/M4A/
M4V/WAV/MP3/FLAC metadata. PDF may use `qpdf`, Ghostscript, or ExifTool when
already present on `PATH`; aiwr does not install or download them.

`--keep-non-ai-metadata` preserves ordinary metadata when possible. Metadata
removal can lose author, date, copyright, or color information. The core does
not promise lossless removal of pixel, waveform, private, or keyed watermarks.

Optional audio remixing requires `ffmpeg` and is lossy:

```bash
aiwr clean-audio speech.wav --remix-audio --audio-tempo 1.08 --audio-pitch 2
```

## 5. Layer B rewriting and detection

Token-sampling watermarks cannot generally be removed by deleting a character.
`rewrite-text` is offline by default:

```bash
aiwr rewrite-text draft.txt --prompt-only

AIWR_REWRITE_PROVIDER=ollama AIWR_REWRITE_MODEL=llama3.2 \
  aiwr rewrite-text draft.txt --output draft.rewritten.txt

AIWR_REWRITE_PROVIDER=openai-compatible OPENAI_API_KEY=... \
  aiwr rewrite-text draft.txt --allow-remote --output draft.rewritten.txt
```

Remote endpoints receive the source text; confirm privacy and authorization
before using `--allow-remote`. Review facts, numbers, code, citations, and
formatting after rewriting. The result is best-effort and does not prove human
authorship or guarantee a detector result.

The local style score and same-key Gumbel check do not modify input:

```bash
aiwr score-stylometry article.txt --json
aiwr detect-gumbel article.txt --key local-secret --json
```

The Gumbel check is meaningful only when the key, tokenizer, and generator PRF
layout are known to match.

## 6. Research workflow and external adapters

```bash
aiwr stealer query --prompts prompts.jsonl --out replies.jsonl --backend dry-run
aiwr stealer build --replies replies.jsonl --out s-star.json
aiwr stealer detect --file candidate.txt --s-star s-star.json
aiwr download-prompts --dataset allenai/c4 --config realnewslike \
  --split train --count 30000 --out ./stealer/prompts
```

`stealer query` is dry-run and offline by default. A remote model needs an
explicit backend, credentials, and `--allow-remote`. The prompt downloader
uses resumable page checkpoints; Ctrl-C returns 130 and preserves the last
complete page.

`score-synthid`, `synthid-score-server`, `synthid-text-server`,
`detect-text-watermark`, `markdiffusion`, `clean-ctrlregen`, and
`bench-synthid-text` are optional adapters, not bundled algorithms. A source
checkout contains the adapter scripts; installed binaries need an explicit
adapter directory via `--upstream-scripts PATH` or `AIWR_UPSTREAM_SCRIPTS`.
You must separately install/configure the third-party checkout, Python
environment, model, or sidecar required by the selected adapter. Adapters do
not download code, weights, Torch, Transformers, or Diffusers automatically.

In particular, aiwr integrates optional MarkLLM and MarkDiffusion backends but
does not reimplement or redistribute their ML runtimes. Missing backends are
reported as unavailable.

## 7. Website auditing

Use this only after explicit authorization to access the website:

```bash
aiwr audit-website --sitemap https://example.com/sitemap.xml --format json
aiwr audit-website --base https://example.com --sarif
```

The reader accepts public HTTP(S) URLs, rejects credentials, private-network
destinations, cross-origin sitemaps, hostile XML entities, and excessive
redirects. It scans remote assets with the Go core and does not execute local
optional tools.

## 8. Hooks, staging, and HTTP

```bash
aiwr check-staged file1.md file2.png
aiwr clean-staged file1.md file2.png
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"note.md"}}' \
  | aiwr hook-written-file --mode check
```

`check-staged` never modifies files. `clean-staged` changes files in place and
requires re-staging. The hook defaults to check mode; clean mode replaces only
when bytes change and does not create a backup.

Start the HTTP service with:

```bash
WATERMARKS_SERVER_API_KEY=change-me aiwr serve
curl -H 'Authorization: Bearer change-me' \
  http://127.0.0.1:8765/capabilities
```

The service provides `/health`, `/capabilities`, `/openapi.json`, `/inspect`,
`/detect`, `/clean`, `/watermark`, and batch variants. File requests use
base64; `/watermark` also accepts text. The service is loopback-only by
default. Configure authentication before exposing it to another host.

The repository's optional Docker image and `compose.yaml` run the native Go
binary with the same API. Compose is a minimal service example, not a research
stack; it does not build or pull third-party ML images.

## 9. Development and limits

```bash
make format
make test
make vet
make smoke
make upstream-check
```

Read [UPSTREAM.md](../UPSTREAM.md) before accepting upstream changes. Do not
add optional research dependencies to the default Go module. See
[AI_AGENT_GUIDE.md](AI_AGENT_GUIDE.md) for the authorization, JSON, exit-code,
and failure-handling contract used by agents.
