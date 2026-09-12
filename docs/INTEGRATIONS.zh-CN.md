# 集成和 Hook

aiwr 有两类集成入口：

1. 原生 Go CLI/HTTP API，供 agent 或编辑器显式调用；
2. 确定性的文件 hook，在工具写入文件后检查，或按明确配置在磁盘上清理。

Hook 不会重写用户即将看到的 assistant 聊天消息；它只能在工具已经写入文件
之后拿到文件路径并处理磁盘上的文件。

## 通用准备

构建或安装 aiwr，先运行 doctor，再用模拟 payload 验证 hook：

选择集成方式前，先运行离线前置条件报告：

```bash
aiwr doctor --json
```

对于 hook 流程，重点查看 `runtime.node` 和 `runtime.python`；后者必须是
Python 3.10 以上。报告也会检查可选适配器脚本以及模型/backend 环境变量。
`missing` 或 `warning` 不代表原生清理失败：只有所选集成需要时才安装或配置，
完成后重新运行 doctor。它不会安装软件包，也不会访问已配置的模型 endpoint。
部署要求所有可选检查完成时，使用 `aiwr doctor --strict --json`。

```bash
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"note.md"}}' \
  | node hooks/run_hook.js --mode check
```

Hook 是本地流程，不依赖 HTTP 服务。它要求 Python 3.10 以上；
`hooks/run_hook.js` 会探测当前平台可用的 `python3`、`python` 或 Windows
`py -3`，并转发 stdin/stdout/stderr。

接受的事件形状是：

```json
{
  "tool_name": "Write",
  "cwd": "/path/to/project",
  "tool_input": { "file_path": "notes.md" }
}
```

`Edit`、`MultiEdit`、`NotebookEdit` 和 `Update` 也接受；notebook 事件可使用
`notebook_path`。相对路径会根据 `cwd` 解析。

默认模式是 `check`：不修改文件，报告发现。`clean` 是明确的原地操作：

```bash
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"note.md"}}' \
  | node hooks/run_hook.js --mode clean
```

模式优先级为命令行 flag、`CLAUDE_PLUGIN_OPTION_HOOK_MODE`、
`WATERMARKS_HOOK_MODE`，最后回退到 `check`。clean 模式会使用同目录临时文件，
只有字节发生变化时才替换；会保留文件权限，不创建 backup。clean hook 后应
重新读取文件，再进行下一次编辑。

Hook 的退出码与普通 CLI 不同：

| 退出码 | 含义 |
| --- | --- |
| `0` | 没有发现，或成功完成非阻塞清理 |
| `2` | 已向 agent 输出需要审查的发现/context |
| `1` | Hook 输入或执行错误 |

这是 `PostToolUse` 流程，不能撤销已经发生的工具调用。仓库门禁请使用
`check-staged` 或 CI。

## Claude Code

仓库根目录包含 Claude Code plugin：

- `.claude-plugin/plugin.json` 声明 plugin 及 `hook_mode` 选项；
- `hooks/hooks.json` 为 `Write|Edit|MultiEdit|NotebookEdit` 注册 `PostToolUse` command；
- `hooks/run_hook.js` 查找 Python 并调用 `service/scripts/hook_written_file.py`。

把仓库作为本地 Claude Code plugin 加载时，以上路径必须保持在一起。安装或
分发前可以先校验 manifest：

```bash
make plugin-validate
```

使用 Claude Code 自带的 plugin manager 添加/加载这个仓库目录。具体的本地
plugin 命令取决于 Claude Code 版本；不要只复制 `.claude-plugin/plugin.json`，
因为 hook 和 service scripts 也是 plugin 的一部分。Anthropic 的事件说明见
[Claude Code hooks 文档](https://docs.anthropic.com/en/docs/claude-code/hooks)。

plugin 加载后，运行上面的模拟 hook 示例，再运行 `aiwr doctor --json`。
doctor 可以验证 Node.js/Python 前置条件，但无法确认某个 Claude Code 版本
是否实际加载了 plugin。

如果只需要 skill（不会注册 hook），可以使用：

```bash
make install-claude-code-skill
```

Plugin hook 默认是 `check`。只有明确要在每次匹配的写入之后原地修改文件时，
才选择 `clean`：

```bash
WATERMARKS_HOOK_MODE=clean claude
```

如果 plugin manager 暴露了 manifest 中的选项，runner 也理解
`CLAUDE_PLUGIN_OPTION_HOOK_MODE=clean`。Hook 在写入之后运行，因此 Claude Code
可能需要重新读取被 hook 修改的文件。Hook 通过 PostToolUse 的 JSON/stdout
契约和 stderr context 报告结果；它不是写入前的 policy gate。

## Grok 和其它 agent 工具

仓库目前为 Grok 提供的是 skill 安装，而不是声称存在某个原生 Grok hook
配置格式：

```bash
make install-skill
```

这会把 `skills/remove-ai-marks` 链接到 `~/.grok/skills/remove-ai-marks`。该
skill 教 agent 调用 HTTP API；它不会在每次文件写入后自动执行。若 Grok 或
其它工具的当前版本支持“command hook + stdin JSON”，可以调用确定性 hook：

```bash
printf '%s' "$EVENT_JSON" \
  | node /absolute/path/to/aiwr/hooks/run_hook.js --mode check
```

事件必须先归一化为上面的通用 payload。如果工具使用不同的事件 schema，请
写一个小 adapter，把它的写入文件路径和工作目录映射为
`tool_input.file_path` 与 `cwd`；不要把 Grok skill 当成原生 post-write hook。
只有明确决定要原地清理时才使用 `--mode clean`。

任何支持 command hook 的编辑器/agent 都可以使用同一个 stdin bridge：

```bash
printf '%s' '{"tool_name":"Write","cwd":"/work","tool_input":{"file_path":"note.md"}}' \
  | node /absolute/path/to/aiwr/hooks/run_hook.js --mode check
```

对于 Cursor，仓库的 `make install-cursor-text-skill` 目标安装文本清理 skill。
`.mdc` 集成文件是指导，不是通用的 post-write hook。使用 Cursor 当前版本的
hook 功能前，请先确认它的事件 payload，再适配到上面的 JSON 契约。

## Git 和 API 集成

与具体工具无关的仓库流程：

```bash
aiwr check-staged path/to/file.md path/to/image.png
aiwr clean-staged path/to/file.md path/to/image.png
```

`check-staged` 只读；`clean-staged` 原地修改文件，之后需要重新暂存。基于服务
的 agent 可以启动 `aiwr serve`，然后参阅 [HTTP API 指南](USER_GUIDE.zh-CN.md)
或打开 [Web UI](WEB_UI.zh-CN.md)。推荐 optional scorer、pixel backend 或研究
适配器前，先检查 `/capabilities`。
