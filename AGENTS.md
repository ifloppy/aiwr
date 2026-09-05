# aiwr AI agent instructions

When an AI agent represents a user while operating aiwr, first read
docs/AI_AGENT_GUIDE.md. That manual defines the stable workflow for
authorization, inspection, cleaning, reporting, and failure handling.

Minimal safety protocol:

1. Process only files, directories, and websites the user owns or explicitly authorizes.
2. Run aiwr inspect --json PATH before deciding whether to clean.
3. Write to a new destination by default; use --in-place only when explicitly requested.
4. A directory defaults to a sibling PATH.cleaned; never put output inside the input directory.
5. Use --json and exit codes for decisions; do not infer success from human-readable text.
6. Do not add --force-text for unknown files automatically; confirm before using remote models, websites, or GPU backends.
7. Re-inspect the output and report residuals, warnings, output paths, and skipped items.
