# 上游跟踪

English version: [UPSTREAM.md](UPSTREAM.md)。

行为基准：[guillaumemeyer/watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover)

| 字段 | 当前值 |
| --- | --- |
| 上游分支 | `main` |
| 最近人工核对 | 2026-09-11 |
| 最近核对 release | `v0.7.0` |
| 自动检查 | `.github/workflows/upstream-sync.yml`，每 30 分钟和手动触发 |
| 本地检查 | `make upstream-check` |

工作流把上游 `main` SHA 写入 `upstream_head:`，变化时发布同步分支。由于仓库策略暂时禁止 Actions 创建 pull request，工作流会在运行摘要中提供手动创建 PR 的链接。SHA 变化不代表行为已经移植；必须审查上游 diff、更新映射并重新运行 Go/Python 测试。

## 目录映射

| 上游模块 | Go 实现/入口 | 说明 |
| --- | --- | --- |
| `service/scripts/text_unicode.py` | `internal/core/text.go` | Layer A 分类、检测、清理 |
| `format_dispatch.py` | `internal/core/classify.go` | 扩展名和 magic bytes 路由 |
| `image_meta.py` | `internal/core/image.go` | 图片元数据和 C2PA/JUMBF |
| `container_meta.py` | `internal/core/container.go`、`pdf.go` | 文档、HTML、SVG、PDF、Markdown、LaTeX |
| `av_meta.py` | `internal/core/av.go` | 音视频容器和 ID3/RIFF |
| `common.py` | `internal/core/io.go`、`process.go` | 限额、原子写、目录镜像、reflink |
| `inspect_file.py`、`clean_file.py` | `cmd/aiwr` | 统一 CLI |
| `rewrite_text.py`、`humanize_pass.py` | `cmd/aiwr/rewrite.go`、`humanize.go` | tactic、strategy、style 和 humanize |
| `server.py` | `internal/core/server.go`、`aiwr serve` | 原生 Go HTTP/OpenAPI/批量/鉴权 |
| `audit_dir.py`、`audit_website.py` | `aiwr audit`、`audit-website` | JSON/SARIF 和公共 URL 审计 |
| `check_staged.py`、`clean_staged.py` | `check-staged`、`clean-staged` | Go 原生暂存区处理 |
| `hook_written_file.py` | `hook-written-file` | Go 原生 PostToolUse hook |
| `detect_gumbel.py`、`score_stylometry.py` | `gumbel.go`、`stylometry.go` | 本地检测/启发式评分 |
| `stealer/*` | `internal/stealer`、`aiwr stealer` | Go tokenizer/scorer/下载器 |
| 研究脚本 | `core.RunExternalCLI` | 显式指定适配器目录，Python/runtime 依赖仍在外部 |

## 责任与发行边界

| 分类 | 内容 | 发行/维护规则 |
| --- | --- | --- |
| A. Native aiwr | `cmd/aiwr`、`internal/core`、`internal/stealer`、Go 测试和 package | 随 Go archive/native package 发行，由 aiwr 维护 |
| B. Optional adapter | `core.RunExternalCLI`、参数/协议 wrapper、sidecar client | 保持小而明确，要求操作者自行提供第三方 backend |
| C. Republished third-party runtime | Python/ML 环境、模型权重、backend Docker image | 不属于 aiwr 正式发行物；不以 aiwr 名义重打包或发布 |
| D. Development/CI | tests、CodeQL、Dependabot、GoReleaser、upstream-sync | 只保护实际维护的代码和打包流程 |
| E. Compatibility/reference | `service/scripts`、skills、benchmark harness、上游映射 | 用于跟踪行为；同步和 Compose 不得使其变成产品 runtime |

## 可执行文件范围

上游处理、检测、审计、service、hook、暂存区和 stealer 入口中属于正式
Go 产品的部分由 `aiwr` 原生实现。仓库可以保留 Python 脚本、skills 和
benchmark 作为兼容/参考资产，但它们出现在 source tree 中不代表属于
release 内容，也不代表 aiwr 维护其 runtime。

原生 Docker image（如果使用）运行 Go binary，只包含原生格式路径需要
的系统工具，不包含 Python 或 ML backend。可选命令必须使用显式提供的
adapter 目录和外部维护的环境。

## 移植规则

1. 先阅读上游 changelog、覆盖矩阵和受影响源码，再写 Go 回归 fixture。
2. 保持未知格式拒绝、输入限额和默认不覆盖原文件等安全语义。
3. Python/GPU/研究依赖只通过显式 adapter 或 sidecar 接入，不混入 MIT 核心或正式 runtime image。
4. 同步 PR 记录已对齐、降级和待移植行为。
5. release 前运行 `go test ./...`、`go vet ./...`、`make smoke` 和目录/reflink/HTTP smoke。

`upstream_head: 41ef353afd8ff354905c25abb803ae964d06d3d9`
