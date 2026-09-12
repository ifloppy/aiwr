# aiwr AI agent 操作手册

English version: [AI_AGENT_GUIDE.md](AI_AGENT_GUIDE.md)。本手册是 AI agent 代用户调用 aiwr 时的操作契约。

## 授权与范围

只处理用户拥有或明确授权的文件、目录和网站。开始前确认处理范围、是否允许新建输出或修改原文件、是否允许访问网站/模型/Hugging Face，以及是否允许有损音频、像素清理或外部后端。

不要把结果描述为人类创作证明或保证检测失败。报告只说明已检测到的确定性标记和元数据；私有、密钥型和像素域标记可能残留。

## 安装和诊断

安装 aiwr 后，先运行离线诊断：

```bash
aiwr doctor
aiwr doctor --json
```

agent 应使用 `--json` 的 `checks` 数组做判断。每项包含稳定的 `id`、
`category`、`status`、`detail`，必要时还会有 `configure` 配置建议。诊断会
检查原生核心、可选系统工具、Python 3.10+、command hook 所需的 Node.js、
适配器脚本、已配置的 backend 目录和 URL、密钥以及 Layer B 模型设置。
它不会安装软件、访问 HTTP endpoint、加载模型权重，也不会发送用户内容。

状态含义如下：

- `ok`：本地前置条件通过检查；
- `missing`：可选工具或配置尚未提供；
- `warning`：配置存在，但离线诊断无法验证 endpoint、软件包或模型是否真的可用；
- `error`：已配置的路径/值无效，或本地探测失败。

`ready=true` 表示原生核心可用。普通 `doctor` 在没有“已配置但错误”的项目时
返回 0，即使可选项目缺失；需要完整可选栈时使用：

```bash
aiwr doctor --strict --json
```

`--strict` 在存在任何 `missing`、`warning` 或 `error` 时返回 1。安装依赖或
修改环境变量后应重新运行 doctor，再重试 agent 集成。不要仅因为输出了
`configure` 就直接执行安装器、`sudo` 或远程健康检查；先获得用户授权，并遵循
所选工具自己的安装说明。

仓库内 agent 集成建议按以下顺序安装：

1. 运行 `aiwr doctor --json` 并保留报告；
2. 安装所需的 agent skill/plugin；使用 Claude Code 时运行
   `make plugin-validate` 校验 plugin；
3. 使用 `hooks/run_hook.js` 时，确认 Node.js 和 Python 3.10+ 通过检查；
4. 按 [INTEGRATIONS.zh-CN.md](INTEGRATIONS.zh-CN.md) 的示例发送模拟
   `PostToolUse` payload；
5. 再次运行 doctor，并报告仍存在的可选缺口。

缺少可选 backend 不会阻止原生 inspect/clean 流程；服务端未配置 Layer B 时，
确定性清理应使用 `options.layer_a_only=true`。

## 标准流程

单文件：

```bash
aiwr inspect --json PATH
aiwr clean --json --output PATH.cleaned.ext PATH
aiwr inspect --json OUTPUT
```

目录：

```bash
aiwr inspect --json DIRECTORY
aiwr clean --json DIRECTORY
aiwr clean --json --output-dir OUTPUT_DIRECTORY DIRECTORY
```

目录输出默认是相邻的 `DIRECTORY.cleaned`，保留相对路径、文件名、权限和符号链接；输出不能等于输入或位于输入内部。未知文件默认原样复制，只有明确需要时才使用 `--skip-unknown`。目录不能使用 `--in-place`。

stdin 文本：

```bash
printf '%s' 'TEXT' | aiwr clean-text -
printf '%s' 'TEXT' | aiwr clean-text --stats -
```

清理后的文本在 stdout，统计 JSON 在 stderr。

## 语言和机器输出

CLI 默认英语。`AIWR_LANG`/`AIWR_LANGUAGE` 优先，其次是 `LC_ALL`、`LC_MESSAGES`、`LANGUAGE`、`LANG`；值以 `zh` 开头时使用中文，其余回退英语。帮助、状态、进度和诊断会本地化；JSON/SARIF 不翻译。外部上游 Python 命令的帮助保持上游原文。

## 退出码

| 退出码 | 含义 | agent 行为 |
| --- | --- | --- |
| 0 | 完成且无可操作残留 | 报告输出路径和简要结果 |
| 1 | 发现命中/残留，或已配置 backend 无效；`doctor --strict` 在可选配置不完整时也返回 1 | 读取 JSON，列出命中、残留、降级或配置错误 |
| 2 | 参数错误、拒绝、单文件不支持或 sitemap 失败 | 修正参数或询问用户，不要原样重复 |
| 3 | 批处理或目录存在部分失败 | 保留成功输出并逐项报告错误 |
| 130 | 用户中断 | 保留原文件/checkpoint，询问是否继续 |

inspect 返回 1 不表示崩溃；未知单文件自动清理会拒绝写入并返回 2。机器决策优先使用 JSON，不要根据人类可读文本猜测成功。

## 选项策略

推荐低风险组合：

```text
--json --only-changed --reflink auto
```

只有在用户请求或确认后使用 `--in-place`、`--nfkc`、`--aggressive-homoglyphs`、`--strip-bidi`、`--strip-emoji-glue`、`--force-text`、`--remix-audio`、`--remove-pixel` 和 `--allow-remote`。不要为了让命令成功而不断放宽拒绝条件。

网站、Ollama、OpenAI-compatible endpoint、Hugging Face、模型、权重和第三方工具都需要相应授权；远程重写会接触原文。服务端文本清理没有配置 Layer B 时可能返回 400，确定性清理应传 `options.layer_a_only=true`。

## 复核与报告

清理后重新 inspect 输出，并报告：输出路径、before/after 命中、残留、warning、退出码和跳过项目。保留原文件和 `.bak`，不要把“已写出”当成“所有水印都已移除”。
