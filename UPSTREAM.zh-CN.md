# 上游跟踪

English version: [UPSTREAM.md](UPSTREAM.md)。

行为基准：[guillaumemeyer/watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover)

| 字段 | 当前值 |
| --- | --- |
| 上游分支 | `main` |
| 最近人工核对 | 2026-09-06 |
| 最近核对 release | `v0.7.0` |
| 自动检查 | `.github/workflows/upstream-sync.yml`，每 30 分钟和手动触发 |
| 本地检查 | `make upstream-check` |

工作流把上游 `main` SHA 写入 `upstream_head:`，变化时创建或更新同步 PR。SHA 变化不代表行为已经移植；必须审查上游 diff、更新映射并重新运行 Go/Python 测试。

## 目录映射

| 上游模块 | Go 实现/入口 | 说明 |
| --- | --- | --- |
| `service/scripts/text_unicode.py` | `internal/core/text.go` | Layer A 分类、检测、清理 |
| `format_dispatch.py` | `internal/core/classify.go` | 扩展名和 magic bytes 路由 |
| `image_meta.py` | `internal/core/image.go` | 图片元数据和 C2PA/JUMBF |
| `container_meta.py` | `internal/core/container.go`、`pdf.go` | 文档、HTML、SVG、PDF、Markdown |
| `av_meta.py` | `internal/core/av.go` | 音视频容器和 ID3/RIFF |
| `common.py` | `internal/core/io.go`、`process.go` | 限额、原子写、目录镜像、reflink |
| `inspect_file.py`、`clean_file.py` | `cmd/aiwr` | 统一 CLI |
| `rewrite_text.py`、`humanize_pass.py` | `cmd/aiwr/rewrite.go`、`humanize.go` | tactic、strategy、style 和 humanize |
| `server.py` | `internal/core/server.go`、`aiwr serve` | HTTP/OpenAPI/批量/鉴权 |
| `audit_dir.py`、`audit_website.py` | `aiwr audit`、`audit-website` | JSON/SARIF 和公共 URL 审计 |
| `check_staged.py`、`clean_staged.py` | `check-staged`、`clean-staged` | Go 原生暂存区处理 |
| `hook_written_file.py` | `hook-written-file` | Go 原生 PostToolUse hook |
| `detect_gumbel.py`、`score_stylometry.py` | `gumbel.go`、`stylometry.go` | 本地检测/启发式评分 |
| `stealer/*` | `internal/stealer`、`aiwr stealer` | Go tokenizer/scorer/下载器 |
| 研究脚本 | `core.RunExternalCLI` | 默认使用内置 `service/scripts`，依赖仍需显式提供 |

## 移植规则

1. 先阅读上游 changelog、覆盖矩阵和受影响源码，再写 Go 回归 fixture。
2. 保持未知格式拒绝、输入限额和默认不覆盖原文件等安全语义。
3. Python/GPU/研究依赖只通过显式 adapter 或 sidecar 接入，不混入 MIT 核心。
4. 同步 PR 记录已对齐、降级和待移植行为。
5. release 前运行 `go test ./...`、`go vet ./...`、`make smoke` 和目录/reflink/HTTP smoke。

`upstream_head: d9e9590d94e19b39eb2794266292324bfec8249a`
