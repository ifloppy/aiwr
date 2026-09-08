# aiwr

`aiwr` 是一个轻量、跨平台的 Go CLI，用于检查和清理自己拥有或获授权处理的内容中的可检测 AI 水印携带物和来源元数据。它参考 [watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover) 的行为，但正式支持的核心由本项目实现和维护。项目采用 MIT 许可证，见 [LICENSE](LICENSE)。

English documentation: [README.md](README.md) · [English user guide](docs/USER_GUIDE.md)

## 功能范围

- Layer A 文本清理：零宽字符、异体空格、双向控制符、选定同形字、NFKC 规范化和 emoji glue 选项。
- PNG、JPEG、WebP、AVIF/HEIC、BMP、GIF、TIFF、SVG、PDF、DOCX/XLSX/PPTX、ODT、EPUB、HTML、Markdown、MP4/MOV/M4A/M4V、WAV、MP3、FLAC 的元数据清理。
- 检查、检测、目录镜像、JSON、SARIF、stdin、暂存区检查、PostToolUse hook、HTTP API、网站审计和 stealer 研究流水线。
- 可选 Layer B 重写：本地 Ollama 或明确授权的 OpenAI-compatible endpoint。默认构建不会下载或嵌入重型模型/GPU 依赖。

仓库也保留 `service/scripts`、skills、hooks 和研究 benchmark 等兼容/参考资产，但它们不属于原生发行运行时。映射与同步边界见 [UPSTREAM.md](UPSTREAM.md)。

## 责任边界

### Native / bundled

Go binary 和常规安装包包含确定性清理器、Unicode/元数据检查、格式路由、本地启发式检测器、CLI/HTTP 接口、审计、hook 和暂存区流程。它们不会安装 Python、Torch、Transformers、Diffusers、模型权重或外部 checkout。

### Optional external backends

`score-synthid`、`detect-text-watermark`、`markdiffusion`、`clean-ctrlregen`、SynthID sidecar 和 MarkLLM benchmark 都是适配器。它们只把 aiwr 参数/协议转换到操作者自行提供的第三方代码，不重实现或重新发行这些算法和 ML runtime。请用 `--upstream-scripts PATH` 或 `AIWR_UPSTREAM_SCRIPTS` 提供适配器目录，再自行安装/配置对应的上游 checkout、Python 环境、模型或 sidecar。缺少 backend 时会明确报告 unavailable，不会自动下载。

## 安装和验证

源码构建需要 Go 1.23 或更新版本：

```bash
go build -trimpath -o aiwr ./cmd/aiwr
go install github.com/iruanp/aiwr/cmd/aiwr@latest
go test ./...
go vet ./...
./aiwr version
```

发行版安装和离线构建见 [packaging/README.md](packaging/README.md)。

## 快速开始

```bash
# 检查，不修改源文件
aiwr inspect draft.txt

# 在源文件旁生成 draft.cleaned.txt
aiwr clean draft.txt

# 将目录镜像到 drafts.cleaned/
aiwr clean ./drafts

# 创建 draft.txt.bak 后原地替换
aiwr clean draft.txt --in-place

# 清理 stdin 文本
printf 'hello\u200bworld\n' | aiwr clean-text -
```

默认输出是新目标：文件为 `NAME.cleaned.EXT`，目录为 `DIRECTORY.cleaned`。未知文件不会被静默当作文本处理；只有确定这种解释正确时才使用 `--as` 或 `--force-text`。

## 主要命令

| 命令 | 作用 |
| --- | --- |
| `clean`、`clean-file` | 自动识别并清理文件或目录 |
| `inspect`、`inspect-file` | 报告元数据、命中和残留风险 |
| `detect` | 输出检测摘要 |
| `audit`、`audit-dir` | 扫描文件或目录，支持 JSON/SARIF |
| `clean-text`、`inspect-text` | 强制文本管线，支持 stdin |
| `clean-image`、`inspect-image` | 强制图片管线 |
| `clean-audio`、`clean-video` | 清理媒体容器，可选有损处理 |
| `score-stylometry` | 本地启发式文体统计 |
| `detect-gumbel` | 复核同 key 的 HMAC/EXP Gumbel 水印 |
| `rewrite-text` | 输出 Layer B prompt 或调用显式 provider |
| `audit-website` | 从同源 sitemap 审计公共 URL |
| `check-staged`、`clean-staged` | 检查或清理仓库文件 |
| `hook-written-file` | 根据 PostToolUse payload 检查或清理文件 |
| `stealer query|build|detect` | 无模型依赖的黑盒研究流水线 |
| `download-prompts` | 下载可恢复的 prompt corpus |
| `serve` | 默认在 `127.0.0.1:8765` 启动 HTTP 服务 |

`score-synthid`、`markdiffusion`、`clean-ctrlregen`、`detect-text-watermark` 等可选研究命令会调用源码 checkout 或其它目录中的适配器脚本，但仍需要相应第三方 checkout、Python 包、模型权重或 sidecar。可用 `--upstream-scripts PATH` 或 `AIWR_UPSTREAM_SCRIPTS` 指定；普通 aiwr 安装不携带这些依赖。

## CLI 语言

默认英语。CLI 的帮助、状态、进度和诊断文本会在检测到中文 locale 时显示中文。优先级如下：

1. `AIWR_LANG` 或 `AIWR_LANGUAGE`（显式覆盖）；
2. `LC_ALL`、`LC_MESSAGES`、`LANGUAGE`、`LANG`。

值以 `zh` 开头时使用中文；不支持、为空或格式异常时回退英语。JSON 和 SARIF 等机器输出保持稳定且不翻译：

```bash
AIWR_LANG=zh-CN aiwr --help
LANG=en_US.UTF-8 aiwr --help
```

委托给上游 Python 命令后的帮助仍由上游命令输出；Go wrapper 和 fallback help 遵循当前语言。

## 安全原则

只处理自己拥有或获授权处理的文件、目录和网站。先检查、默认写到新目标，再检查输出：

```bash
aiwr inspect --json input.md > before.json || test $? -eq 1
aiwr clean --json --output input.cleaned.md input.md
aiwr inspect --json input.cleaned.md > after.json || test $? -eq 1
```

`--in-place`、`--force-text`、`--nfkc`、激进同形字处理、`--strip-bidi`、`--strip-emoji-glue`、音频重混、像素清理、远程模型调用以及网站/prompt 下载都应在明确选择后使用。AI agent 代操作前请阅读 [docs/AI_AGENT_GUIDE.md](docs/AI_AGENT_GUIDE.md)。项目不承诺移除私有、密钥型或像素域水印，不承诺让检测器失败，也不能证明人类创作。

## HTTP API 和开发

服务提供 `/health`、`/capabilities`、`/openapi.json`、`/inspect`、`/detect`、`/clean`、`/watermark` 及批量接口。文件内容使用 base64；文本 watermark 接口也接受 `text`。对外暴露前请设置 `WATERMARKS_SERVER_API_KEY` 或 `WATERMARKS_API_KEY`。请求格式、限制、鉴权和 sidecar 见 [docs/USER_GUIDE.md](docs/USER_GUIDE.md)。

可选容器镜像和 [compose.yaml](compose.yaml) 运行同一个 Go binary。Compose 只是最小 native 服务示例，不是 research stack，不会构建或拉取 MarkLLM、MarkDiffusion、CtrlRegen 或 SynthID runtime。目前正式发布到 GHCR 的容器镜像仅面向 `linux/amd64`；原生归档和系统包仍提供 arm64。核心镜像故意不包含 Python/ML runtime；适配器应使用操作者维护的主机环境或第三方 sidecar/service。

```bash
make format
make test
make vet
make smoke
make upstream-check
```

上游 `main` commit 记录在 [UPSTREAM.md](UPSTREAM.md)；同步前请审查上游 diff 并运行完整测试。
