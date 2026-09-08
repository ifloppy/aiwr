# aiwr AI agent operation guide

Chinese translation: [AI_AGENT_GUIDE.zh-CN.md](AI_AGENT_GUIDE.zh-CN.md).

This guide is for an AI agent that calls aiwr on a user's behalf. It is an
operational contract rather than an implementation reference. Invoke commands
with an argument array when possible; never interpolate a user-provided path
into an unescaped shell string.

## 1. Responsibility and authorization

Use aiwr only for content the user owns or explicitly authorizes. Before
starting, confirm:

- which files, directories, or websites are in scope;
- whether creating new files, a sibling .cleaned directory, or changing originals is allowed;
- whether network access to websites, Ollama, OpenAI-compatible endpoints, or Hugging Face is allowed;
- whether lossy audio remixing, pixel cleaning, or model backends are allowed.

Never describe a result as proof of human authorship or as a guarantee that a
detector will fail. The result only describes the deterministic marks or
metadata listed in the report; private, keyed, or pixel-domain marks may remain.

## 2. Installation and package selection

If aiwr is not on PATH, inspect the platform before proposing an install:

    command -v aiwr
    cat /etc/os-release

Do not run package installation or sudo without user approval. Prefer a signed
release artifact and its checksums over an unverified binary or a curl-to-shell
installer. Select the package by platform:

- Debian, Ubuntu, Mint, and Pop!_OS: install the matching .deb with apt;
- Fedora, RHEL, CentOS Stream, Rocky, Alma, and openSUSE: install the matching
  .rpm with dnf, yum, or zypper;
- Arch and Manjaro: install the matching .pkg.tar.zst with pacman;
- Alpine: install the matching .apk with apk;
- macOS and Windows: use the matching signed release archive and put aiwr on PATH.

After installation, verify:

    aiwr version
    aiwr help

If no release artifact matches the platform, report that fact and offer the
documented Go or nFPM build path. Do not silently install Go, nFPM, models, or
system tools as a side effect of a cleaning request.

## 3. Standard decision flow

### One file

Inspect without modifying the input:

    aiwr inspect --json PATH

Read kind, suspicious_total, findings, layer_a_hits, text, synthid, and notes.
If cleaning is authorized, write to an explicit new target:

    aiwr clean --json --output PATH.cleaned.ext PATH

The output flag can be omitted to let aiwr create NAME.cleaned.EXT. Inspect the
output again with aiwr inspect --json OUTPUT and report before/after results
together.

### Directory

Inspect the whole directory first:

    aiwr inspect --json DIRECTORY

Then use:

    aiwr clean --json DIRECTORY

The output is the sibling directory DIRECTORY.cleaned. Relative paths, file
names, directory modes, and symbolic links are preserved. For an explicit
destination:

    aiwr clean --json --output-dir OUTPUT_DIRECTORY DIRECTORY

The output directory must not equal the input or be inside it. Unknown files in
a directory are copied unchanged by default to preserve the tree. Use
--skip-unknown only when the user explicitly wants them omitted. Never use
--in-place for a directory.

### Text through stdin

For user-provided text without file metadata:

    printf '%s' 'TEXT' | aiwr clean-text -

For machine-readable cleaning counts:

    printf '%s' 'TEXT' | aiwr clean-text --stats -

Cleaned text is on stdout and statistics JSON is on stderr; do not treat stderr
as the text result.

### Websites

Use only after explicit network authorization:

    aiwr audit-website --sitemap https://example.com/sitemap.xml --format json

The command audits public HTTP(S) URLs and does not run local optional tools.
If a URL download, cross-origin sitemap, private-network resolution, or redirect
is rejected, report the reason instead of weakening the safety check.

### HTTP service

Check service health and capabilities before sending content:

    curl -fsS http://127.0.0.1:8765/health
    curl -fsS http://127.0.0.1:8765/capabilities

Before /clean, confirm authentication and the Layer B policy. Without a
configured model backend, server-side text cleaning may return 400; for
deterministic cleaning, send options.layer_a_only=true. `/watermark` and
`/watermark/batch` send prompts to the configured SynthID text sidecar or
MarkLLM backend; check capabilities and obtain explicit authorization before
sending original text to a remote endpoint.

## 4. Exit codes and report decisions

| Exit code | Meaning | Agent action |
| --- | --- | --- |
| 0 | Completed with no actionable residual | Report output paths and a short result |
| 1 | A hit/residual was found, or an optional backend is unavailable | Read JSON and list the hit, residual, or degradation |
| 2 | Invalid arguments, refusal, unsupported single file, or sitemap failure | Correct arguments or ask the user; do not repeat unchanged |
| 3 | A batch or directory contains partial failures | Keep successful outputs and report each error |
| 130 | User interrupted the operation | Preserve originals/checkpoints and ask whether to continue |

An inspect exit code of 1 is not a crash; it means a signal needs attention.
A single unknown file passed to automatic clean is refused without writing and
returns 2. Batch failures usually return 3. Prefer JSON over human-readable
stdout/stderr for decisions.

## 5. Option policy

Prefer this low-risk combination:

    --json --only-changed --reflink auto

Use the following only after user request or confirmation:

- --in-place: changes the original and creates FILE.bak the first time;
- --nfkc and --aggressive-homoglyphs: may change letters or symbols;
- --strip-bidi and --strip-emoji-glue: may change valid layout or emoji sequences;
- --keep-non-ai-metadata: preserves ordinary metadata such as author or color information;
- --force-text: interprets binary-looking input as text;
- --remix-audio and --remove-pixel: may alter quality, dimensions, or encoding;
- --allow-remote: permits sending text to a non-loopback model endpoint.

Do not keep widening flags just to make a command succeed. Report unknown formats
and do not blindly send binary files through a text pipeline.

## 6. Optional backends and network boundaries

score-synthid, synthid-score-server, synthid-text-server, detect-text-watermark,
markdiffusion, and clean-ctrlregen are optional adapters. A source checkout can
provide the adapter scripts under service/scripts; installed binaries require
an explicit --upstream-scripts directory. The third-party checkout, Python
environment, weights, or sidecar must be installed/configured by the operator.
These adapters do not download code, weights, or dependencies automatically.
Pass through and report available:false, error, and partial instead of hiding
them. `WATERMARKS_SYNTHID_TEXT_URL` is a network boundary: report when a
watermark request leaves the machine.

rewrite-text changes wording and can affect facts, code, numbers, citations, and
formatting. Explain this before using a model; prefer --prompt-only or local
Ollama. A remote provider requires user authorization and the response should
say that the text left the machine.

## 7. Delivery checklist

After each operation, answer briefly with:

1. paths inspected and processed;
2. output path, or why nothing was written;
3. exit code and meaning;
4. changed count, hits, residuals, warnings, skipped items, and errors;
5. whether network, models, external tools, or lossy backends were used;
6. whether post-clean inspection passed.

If the user did not request replacement, do not suggest deleting originals.
Keep originals, JSON reports, and .bak files until the user confirms the result.
