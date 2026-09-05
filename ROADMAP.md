# aiwr Roadmap

路线图以 Go 重实现长期跟随 `guillaumemeyer/watermarks-remover` 为主线。条目分为“行为复刻”（必须与上游持续对齐）和“Go 原生增强”（不改变上游语义）。上游变化以同步 PR 进入，而不是静默拉取运行时代码。

## 已完成

- [x] 统一 `clean` / `inspect` / `detect` / `audit` CLI 和格式强制别名。
- [x] Layer A Unicode 检查、统计、NFKC、空格/同形字/Bidi/emoji 选项。
- [x] PNG、JPEG、WebP、AVIF/HEIC、BMP、GIF、TIFF/BigTIFF metadata path。
- [x] SVG、PDF fallback、OOXML、ODT、EPUB、HTML、Markdown 容器 path。
- [x] MP4/MOV/M4A/M4V、WAV、MP3、FLAC metadata path；ffmpeg audio remix hook。
- [x] stdin、JSON、SARIF、unknown refusal、输入大小上限和二进制误判保护。
- [x] 安全的原子写入、`.bak` 保留、符号链接边界和目录镜像输出。
- [x] Linux FICLONE reflink `auto|always|never`。
- [x] 与上游路径对应的 HTTP API、OpenAPI 文档、批量上限和 Bearer 鉴权。
- [x] Layer B `print-prompt`、Ollama、OpenAI-compatible 基础适配器。
- [x] `audit-website` Go 原生 sitemap/远程资产审计，含公共 IP 固定连接、同源校验、DTD/实体拒绝、JSON/SARIF。
- [x] `check-staged`、`clean-staged`、`hook-written-file` Go 原生集成命令；Hook 只在内容变化时替换并保留权限。
- [x] `stealer query|build|detect` 与 `download-prompts` Go 原生研究编排；下载器按页 checkpoint、可中断恢复。
- [x] Go 单元/集成测试、CI 和每 30 分钟上游同步 PR 触发器。
- [x] AI agent 操作手册、根目录 AGENTS 协议和安装后文档布局。
- [x] nFPM 统一生成 Deb/RPM/APK/Arch Linux 包，GoReleaser 生成跨平台归档和 release 产物。

## 近期：行为对齐

- [x] 让 `/clean` 使用可配置 strategy（`paraphrase@0.8,mlm@0.2`）并在 provider 未配置时与上游的拒绝语义一致；保留明确的 Layer-A-only 选项。MLM 仍明确报告为可选外部后端。
- [x] 把上游 `rewrite_text.py` 的 tactic、rewrite level、style、back-translation、humanize 和 chunk 策略补到 Go API/CLI；模型调用仍是显式 provider。
- [x] 增加 `detect_gumbel` 的 HMAC/EXP same-key 复核与不泄漏密钥的 JSON 报告。
- [x] 增加 MarkLLM 外部 checkout 适配器、scheme/timeout/离线可用性报告和 rewrite 透传。
- [x] 增加 `/detect` 的 text stylometry 与 optional detector result schema，并统一 `detect_before`/`detect_after`。
- [x] 增加 `/watermark`、`/watermark/batch` 文本生成接口；sidecar/local MarkLLM 路由、超时、批处理 fail-soft、OpenAPI 和显式 `synthid-text-server` adapter。
- [x] 实现 PDF qpdf/ExifTool/Ghostscript 分级清理、`deep_images=auto|always|lossless|never` 的证据驱动路径。
- [x] 把 EPUB encrypted parts、OOXML metadata/relationship/content-types、截断媒体尾部等上游回归案例逐项加入 fixture。

## 中期：可插拔重型后端

- [x] 为 SynthID HTTP sidecar 增加仅检测、fail-soft、endpoint 鉴权和 before/after score；本地 reverse-SynthID checkout 仍需外部适配器。
- [x] 增加 CtrlRegen adapter（外部进程，明确质量损失和模型许可证，不进入默认构建）。
- [x] 增加 MarkDiffusion/DiffusionPurification adapter（外部进程，明确质量损失和模型许可证，不进入默认构建）。
- [x] 通过上游 `clean_video.py` adapter 提供 TrustMark per-frame video purification、temporal vote 和原始文件保留；Go CLI 透传该入口，模型仍需显式外部 backend。
- [x] 增加 audio remix chain 的可配置 codec、ffprobe 采样率和独立临时目标；波形水印本身仍不作无损保证。
- [x] 增加 watermark-stealing 的 Go orchestrator；`bench-synthid-text` 仍通过显式外部依赖运行，避免把 benchmark/模型装入默认构建。

## 长期：工程质量

- [ ] Go fuzzing 覆盖 PNG/JPEG/WebP/TIFF/ISOBMFF/RIFF/ZIP 截断、压缩炸弹和整数溢出。
- [ ] Windows/macOS reflink 能力探测和跨平台权限/符号链接测试。
 - [ ] 可选 worker pool，在不改变目录顺序和错误聚合的前提下真正使用 `--jobs`。
- [ ] 生成版本化 JSON Schema/OpenAPI client 和稳定的 semver 兼容策略。
- [x] 增加基础 reproducible release、静态二进制、Deb/RPM/APK 发行包和跨平台归档；
      签名校验、SBOM、Homebrew/winget 官方渠道仍待补齐。
- [ ] 每次上游 release 自动生成 coverage diff、兼容性报告和迁移说明。

## 当前明确边界

- `score-synthid`、`synthid-score-server`、`synthid-text-server`、`detect-text-watermark`、`markdiffusion`、`clean-ctrlregen`、`bench-synthid-text` 仍保留 CLI 入口；源码 checkout 默认透传仓库内置 `service/scripts`，也可通过 `--upstream-scripts`/环境变量指定其它上游脚本或模型环境；这属于依赖边界，不是静默降级。
- `audit-website`、暂存区命令、PostToolUse hook、stealer scorer/download 已不依赖 Python；这些路径的行为由 Go 测试直接覆盖。
- 未完成条目主要集中在 TrustMark 逐帧 backend、fuzz/跨平台发布和可复现发行工程，不会改变 MIT 核心的默认离线/安全写入策略。

## 不会承诺

- 不承诺移除未知的私有/密钥型水印。
- 不承诺官方厂商检测器一定失败。
- 不默认联网下载模型、执行第三方代码或修改用户原文件。
- 不把“清理成功”解释为“证明人类创作”或取消署名/披露义务。
