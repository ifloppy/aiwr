# Integrations and hooks

aiwr has two different integration surfaces:

1. the native Go CLI/HTTP API, which an agent or editor can call explicitly;
2. a deterministic file hook, which runs after a tool writes a file and can
   report or clean that file on disk.

The hook does not rewrite an assistant's chat message. It only sees a file
path after a tool call has already written it.

## Common setup

Build or install aiwr, run the doctor, then verify the hook with a synthetic
payload:

Before selecting an integration, run the offline prerequisite report:

```bash
aiwr doctor --json
```

For the hook path, the important checks are `runtime.node` and
`runtime.python`; the latter must be Python 3.10+. The report also covers the
optional adapter scripts and model/backend environment variables. A `missing`
or `warning` entry is not a failure of the native cleaner: install or configure
it only if the selected integration needs it, then rerun the doctor. It never
installs packages or contacts configured model endpoints. Use
`aiwr doctor --strict --json` when a deployment requires every optional check
to be complete.

```bash
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"note.md"}}' \
  | node hooks/run_hook.js --mode check
```

The hook is local and does not need the HTTP service. It expects a Python 3.10+
interpreter; `hooks/run_hook.js` probes the platform's available `python3`,
`python`, or Windows `py -3` and forwards stdin/stdout/stderr.

The accepted event shape is:

```json
{
  "tool_name": "Write",
  "cwd": "/path/to/project",
  "tool_input": { "file_path": "notes.md" }
}
```

`Edit`, `MultiEdit`, `NotebookEdit`, and `Update` are also accepted. A
`notebook_path` is accepted for notebook events. Relative paths are resolved
against `cwd`.

The default mode is `check`: it leaves the file alone and reports findings.
`clean` is an explicit in-place operation:

```bash
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"note.md"}}' \
  | node hooks/run_hook.js --mode clean
```

The CLI flag wins, followed by `CLAUDE_PLUGIN_OPTION_HOOK_MODE`, then
`WATERMARKS_HOOK_MODE`, then `check`. Clean mode swaps a sibling temporary file
only when bytes changed, preserves file permissions, and does not create a
backup. Re-read the file after a clean hook before applying another edit.

Hook exit codes are intentionally different from the ordinary CLI exit code:

| Exit | Meaning |
| --- | --- |
| `0` | No finding, or a successful non-blocking clean |
| `2` | Findings/context were written for the agent to review |
| `1` | Hook input or execution error |

Because this is a `PostToolUse` flow, it cannot undo the tool call that already
happened. Use `check-staged` or CI for a repository gate.

## Claude Code

This repository contains a Claude Code plugin at the repository root:

- `.claude-plugin/plugin.json` declares the plugin and its `hook_mode` option;
- `hooks/hooks.json` registers a `PostToolUse` command for `Write|Edit|MultiEdit|NotebookEdit`;
- `hooks/run_hook.js` finds Python and invokes `service/scripts/hook_written_file.py`.

Keep those paths together when loading the repository as a local Claude Code
plugin. Validate the manifest before installing or distributing it:

```bash
make plugin-validate
```

The plugin directory must remain available as a local checkout. After loading
it, run the synthetic hook example above and then `aiwr doctor --json`; the
doctor can verify the Node.js/Python prerequisites, but it cannot verify whether
a particular Claude Code version has loaded the plugin.

Use Claude Code's plugin manager to add/load this repository directory. The
exact local-plugin command is version-dependent; do not copy only
`.claude-plugin/plugin.json`, because the hook and service scripts are part of
the plugin. The official hook event reference is maintained by Anthropic in
[Claude Code hooks documentation](https://docs.anthropic.com/en/docs/claude-code/hooks).

For a skill-only install, which does not register a hook, use:

```bash
make install-claude-code-skill
```

The plugin hook is `check` by default. Choose `clean` only when modifying the
file in place after every matching write is intended:

```bash
WATERMARKS_HOOK_MODE=clean claude
```

If the plugin manager exposes the declared option, `CLAUDE_PLUGIN_OPTION_HOOK_MODE=clean`
is also understood by the runner. The hook runs after the write, so Claude
Code may need to re-read a file that the hook changed. It reports findings via
the PostToolUse JSON/stdout contract and stderr context; it is not a
pre-write policy gate.

## Grok and other agent tools

The repository currently provides Grok a skill installation, not a claimed
native Grok hook schema:

```bash
make install-skill
```

This links `skills/remove-ai-marks` into `~/.grok/skills/remove-ai-marks`. The
skill teaches the agent to call the HTTP API; it does not run after every file
write. Grok or another tool can use the deterministic hook if its version
supports command hooks that pass JSON on stdin. Configure that tool's command
to invoke:

```bash
printf '%s' "$EVENT_JSON" \
  | node /absolute/path/to/aiwr/hooks/run_hook.js --mode check
```

The event must be normalized to the common payload above. If the tool exposes a
different event schema, write a small adapter that maps its written-file path
and working directory to `tool_input.file_path` and `cwd`; do not assume that a
Grok skill is the same thing as a native post-write hook. Use `--mode clean`
only as an explicit policy decision.

The same stdin bridge works for any editor/agent that supports command hooks:

```bash
printf '%s' '{"tool_name":"Write","cwd":"/work","tool_input":{"file_path":"note.md"}}' \
  | node /absolute/path/to/aiwr/hooks/run_hook.js --mode check
```

For Cursor, the repository's `make install-cursor-text-skill` target installs
the text-cleaning skill. The `.mdc` integration file is guidance, not a
universal post-write hook. Use Cursor's version-specific hook facility only
after confirming its event payload and adapt it to the JSON contract above.

## Git and API integrations

For a tool-agnostic repository workflow:

```bash
aiwr check-staged path/to/file.md path/to/image.png
aiwr clean-staged path/to/file.md path/to/image.png
```

`check-staged` is read-only; `clean-staged` changes files in place and requires
re-staging. For service-based agents, start `aiwr serve` and follow the
[HTTP API guide](USER_GUIDE.md) or open the [Web UI](WEB_UI.md). Check
`/capabilities` before recommending optional scorers, pixel backends, or
research adapters.
