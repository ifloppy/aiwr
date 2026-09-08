# aiwr 路线图

English version: [ROADMAP.md](ROADMAP.md)。路线图跟随 `guillaumemeyer/watermarks-remover`，并将 Go 原生实现/安全增强与兼容性移植分开。上游变化通过审查后的同步 PR 进入，不静默下载运行时代码。

## 已完成

- [x] 统一 `clean`、`inspect`、`detect`、`audit` 和格式强制别名。
- [x] Layer A Unicode 检查、统计、NFKC、空格、同形字、bidi、emoji 选项。
- [x] 图片、文档/容器、音视频元数据管线和 PDF fallback。
- [x] stdin、JSON、SARIF、未知格式拒绝、大小限制和二进制保护。
- [x] 原子写入、`.bak`、符号链接边界、目录镜像和 Linux FICLONE reflink。
- [x] HTTP API、OpenAPI、批量限制、Bearer 鉴权和 Layer B strategy 路由。
- [x] Layer B prompt/Ollama/OpenAI-compatible 适配器、本地文体统计、同 key Gumbel、MarkLLM 路由和文本 watermark gateway。
- [x] Go 原生网站审计、暂存区命令、PostToolUse hook、stealer、可恢复 prompt 下载器、CI、打包、上游同步和中英双语文档。
- [x] CLI 默认英语，并按 locale 显示中文或回退英语。

## 剩余工程工作

- [ ] fuzz PNG/JPEG/WebP/TIFF/ISOBMFF/RIFF/ZIP 截断、压缩炸弹和整数溢出边界。
- [ ] Windows/macOS reflink 探测及跨平台权限/符号链接测试。
- [ ] 在保持目录顺序和错误聚合的前提下真正使用 `--jobs` worker pool。
- [ ] 版本化 JSON Schema/OpenAPI client 和稳定 semver 策略。
- [ ] 每个上游 release 自动生成 coverage diff、兼容报告和迁移说明。
- [ ] 签名 release、SBOM、Homebrew/winget 官方渠道。

## 明确边界

研究命令作为可选 adapter 保留上游入口，但 checkout、Python 包、模型权重和 sidecar 仍是操作者必须显式提供的依赖。源码 checkout 可以提供 `service/scripts` 中的 adapter；安装后的 binary 必须用 `--upstream-scripts` 或 `AIWR_UPSTREAM_SCRIPTS` 显式指定目录。aiwr 不重新发行第三方 ML runtime。网站审计、暂存区、hook、stealer 评分和 prompt 下载不需要 Python。

## 不承诺

- 不承诺移除未知的私有或密钥型水印。
- 不承诺官方厂商检测器一定失败。
- 默认不下载模型、不执行第三方代码、不上传网络，也不修改原文件。
- 不将清理成功解释为人类创作证明，也不取消署名和披露义务。
