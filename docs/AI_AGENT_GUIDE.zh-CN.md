# aiwr AI agent 操作手册

English version: [AI_AGENT_GUIDE.md](AI_AGENT_GUIDE.md)。本手册是 AI agent 代用户调用 aiwr 时的操作契约。

## 授权与范围

只处理用户拥有或明确授权的文件、目录和网站。开始前确认处理范围、是否允许新建输出或修改原文件、是否允许访问网站/模型/Hugging Face，以及是否允许有损音频、像素清理或外部后端。

不要把结果描述为人类创作证明或保证检测失败。报告只说明已检测到的确定性标记和元数据；私有、密钥型和像素域标记可能残留。

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
| 1 | 发现命中/残留，或可选后端不可用 | 读取 JSON，列出命中、残留或降级 |
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
