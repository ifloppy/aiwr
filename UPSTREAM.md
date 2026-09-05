# Upstream tracking

行为基准：[guillaumemeyer/watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover)

| 字段 | 当前值 |
| --- | --- |
| 上游分支 | `main` |
| 最近人工核对 | 2026-09-06 |
| 最近核对 release | `v0.7.0` |
| 自动检查 | `.github/workflows/upstream-sync.yml`，每 30 分钟 + 手动触发 |
| 本地检查 | `make upstream-check` |

工作流把上游 `main` 的 SHA 写入本文件的 `upstream_head:` 行，并创建/更新同步 PR。同步 PR 必须重新运行 Go 测试、审查上游 diff、更新本表和 [ROADMAP.md](ROADMAP.md)；只更新 SHA 不表示行为已经复刻。

## 目录映射

| 上游模块 | Go 实现/入口 | 备注 |
| --- | --- | --- |
| `service/scripts/text_unicode.py` | `internal/core/text.go` | Layer A 字符分类、检测、清理 |
| `format_dispatch.py` | `internal/core/classify.go` | 扩展名 + magic bytes |
| `image_meta.py` | `internal/core/image.go` | 图片元数据和 C2PA/JUMBF |
| `container_meta.py` | `internal/core/container.go` + `internal/core/pdf.go` | 文档、HTML、SVG、PDF、Markdown；可选 qpdf/Ghostscript/ExifTool |
| `av_meta.py` | `internal/core/av.go` | 音视频容器和 ID3/RIFF |
| `common.py` | `internal/core/io.go`、`process.go` | 限额、原子写、目录镜像、reflink |
| `inspect_file.py` / `clean_file.py` | `cmd/aiwr` + `internal/core/process.go` | unified CLI |
| `inspect_text.py` / `clean_text.py` | `aiwr inspect-text` / `clean-text` | 支持 stdin |
| `inspect_image.py` / `clean_image.py` | `aiwr inspect-image` / `clean-image` | 支持 listed image formats |
| `rewrite_text.py` + `humanize_pass.py` | `cmd/aiwr/rewrite.go` + `internal/core/humanize.go` | tactic、strategy、style、候选评估和 deterministic humanize |
| `server.py` | `internal/core/server.go` + `aiwr serve` | HTTP/OpenAPI/batch/auth；含 `/watermark` 文本生成网关 |
| `text_watermark.py` | `internal/core/textwatermark.go` | 文本输入、keys/options 校验、sidecar/local MarkLLM 路由；模型仍为显式外部依赖 |
| `synthid_text_server.py` | `aiwr synthid-text-server` | 显式转发上游文本 SynthID sidecar；不把模型/权重嵌入 Go 核心 |
| `audit_dir.py` | `aiwr audit` | JSON/SARIF |
| `audit_website.py` | `aiwr audit-website` | Go 原生 sitemap/公共 URL 审计；远程资产不执行本机可选工具 |
| `clean_audio.py` / `clean_video.py` | `clean-audio` / `clean-video` + ffmpeg hook | metadata path 已内置；波形/逐帧后端见 roadmap |
| `check_staged.py` | `aiwr check-staged` | Go 原生批量检查，复用统一 inspect/actionability |
| `clean_staged.py` | `aiwr clean-staged` | Go 原生原地批处理，保留 `.bak` 和退出码语义 |
| `hook_written_file.py` | `aiwr hook-written-file` | Go 原生 PostToolUse stdin JSON；只在变化时原子替换 |
| `detect_gumbel.py` | `internal/core/gumbel.go` | HMAC/EXP same-key 复核；真实 tokenizer/模型 key 仍需由调用方提供 |
| `score_stylometry.py` | `internal/core/stylometry.go` | 本地启发式 score，短文本报告 uncalibrated |
| `stealer/steal.py`, `scorer.py`, `tokens.py` | `internal/stealer` + `aiwr stealer` | Go 原生 tokenizer/scorer/query/build/detect；目标模型仍由调用方提供 |
| `stealer/download_prompts.py` | `internal/stealer` + `aiwr download-prompts` | Go 原生 datasets-server 分页/状态恢复/中断回滚 |
| `score_synthid.py`, `synthid_score_server.py`, `detect_text_watermark.py`, `markdiffusion_harness.py`, `clean_ctrlregen.py`, `bench_synthid_text.py` | `core.RunExternalCLI` | 默认从仓库内置 `service/scripts` 透传，也可用 `--upstream-scripts` 指定外部目录；研究依赖/权重不进默认二进制 |
| MarkLLM, SynthID, CtrlRegen, MarkDiffusion | Go 参数/协议适配 + 外部可选后端 | 不自动下载模型或代码；可用性/timeout/残留在报告中显式表达 |

## 可执行文件的范围

`aiwr` 对齐上游的水印处理、检测、审计、服务、hook、暂存区和 stealer
命令入口；这些路径默认由 Go 实现。仓库同时保留上游的 Agent Skills、安装器、
Claude 插件、pre-commit、Docker/Compose、bootstrap 和 benchmark 入口，保证
原项目的仓库级工作流仍可调用。需要第三方模型/权重的研究后端仍不嵌入 Go
二进制，而是由 `service/scripts` 中的兼容适配器显式调用用户自己的 checkout
或 sidecar。

## 移植规则

1. 先读取上游 changelog、README coverage matrix 和受影响源码，再在 Go 中写回归 fixture。
2. 保持上游的安全拒绝语义：未知格式不自动当文本；截断/压缩上限不应导致无限内存；默认不覆盖原文件。
3. 上游可选 Python/GPU/研究依赖只通过显式 adapter 或 sidecar 接入，不把其代码、权重或许可证混入 MIT 核心。
4. 在同步 PR 中记录“已对齐、降级、待移植”三类行为，并更新用户手册和路线图。
5. release 前运行 `go test ./...`、`go vet ./...`、`make smoke` 和目录/reflink/HTTP smoke。
6. 上游 stealer/website/staged/hook 行为变化时，优先更新对应 Go fixture；不要把外部脚本路径当成默认运行时依赖。

upstream_head: d9e9590d94e19b39eb2794266292324bfec8249a
