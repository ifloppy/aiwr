---
name: Bug report
about: Report a defect in aiwr (native CLI/service, adapters, or docs)
title: "[bug] "
labels: bug
assignees: ""
---

## What happened

A clear description of the unexpected behaviour.

## What you expected

What should have happened instead.

## Steps to reproduce

1.
2.
3.

## Environment

- OS and arch:
- aiwr version (`aiwr version`):
- How you run aiwr (native binary / `go run` / service / adapter):
- Optional tools present (`c2patool`, `exiftool`) and versions if relevant:

## Input type

- [ ] Text (paste / `.txt` / `.md` / other)
- [ ] Image (PNG / JPEG)
- [ ] Both / batch directory
- Layer involved: A (Unicode) / B (rewrite guidance) / Files (C2PA/metadata)

## Diagnostics

Paste relevant CLI output (redact private content):

```bash
aiwr inspect --json path
# or, for an explicitly configured optional adapter:
aiwr detect-text-watermark --upstream-scripts PATH --help
```

## Extra context

Sample files (if shareable), screenshots, or related issues. Do not paste secrets, private documents, or material you do not own.
