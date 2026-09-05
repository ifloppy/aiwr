# aiwr

`aiwr` 是 [guillaumemeyer/watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover) 的 Go 语言复刻版，命令行名为 `aiwr`。它面向自己拥有或获授权处理的内容，清理可验证的 Unicode 携带物和文件元数据；项目以 MIT 许可证发布，见 [LICENSE](LICENSE)。

本项目保留上游的两层模型：

| 层 | 目标 | aiwr 的处理方式 |
| --- | --- | --- |
| Layer A | 零宽字符、双向控制符、标签字符、异体空格、部分同形字 | 本地、确定性、默认启用 |
| Layer B | token 采样型统计水印 | `rewrite-text` 调用可选 Ollama/OpenAI-compatible 服务；不能证明绕过任何厂商检测 |
| 文件层 | C2PA、JUMBF、XMP、EXIF、文档属性和媒体容器标记 | Go 标准库优先的二进制/容器清理器 |

## 当前覆盖

统一命令按扩展名和 magic bytes 路由：

- 文本：UTF-8/不规则 UTF-8 字节保留、NFKC、空格和同形字处理、Bidi/emoji glue 选项。
- 图片：PNG、JPEG、WebP、AVIF、HEIC、BMP、GIF、TIFF/BigTIFF。
- 容器：SVG、PDF（内置 fallback，并自动利用 PATH 中可用的 qpdf/Ghostscript/ExifTool）、DOCX、XLSX、PPTX、ODT、EPUB、HTML、Markdown。
- 音视频：MP4、MOV、M4A、M4V、WAV、MP3、FLAC；支持容器元数据清理，音频可选 ffmpeg 重编码。
- 服务：`/health`、`/capabilities`、`/openapi.json`、`/inspect`、`/detect`、`/clean`、`/watermark` 及四个批量接口。
- 目录：输出到相邻的 `<目录>.cleaned`，保留相对路径、文件名、权限和符号链接；没有变化的文件会优先使用 Linux `FICLONE` reflink，失败后回退普通复制。

上游可选的 GPU/研究后端（CtrlRegen、MarkDiffusion、MarkLLM、SynthID 等）不被偷偷下载或嵌入核心二进制：轻量编排和协议适配已在 Go 中提供，真正的模型/权重仍需显式 checkout、sidecar 或外部工具。仓库也保留 `service/scripts` 中上游要求的兼容入口，未配置时不会自动执行重型后端。SynthID HTTP scorer 和文本生成 sidecar 分别可通过 `WATERMARKS_SYNTHID_SCORER_URL`、`WATERMARKS_SYNTHID_TEXT_URL` 接入。上游功能映射和同步规则见 [UPSTREAM.md](UPSTREAM.md)。

## 安装

需要 Go 1.23 或更新版本。核心只依赖 `golang.org/x/text` 的 Unicode NFKC 实现。

```bash
go build -trimpath -o aiwr ./cmd/aiwr
# 或
go install github.com/iruanp/aiwr/cmd/aiwr@latest
```

验证：

```bash
go test ./...
go vet ./...
./aiwr version
```

## 快速使用

```bash
# 检查，不修改原文件；发现 Layer A 命中时退出码为 1
aiwr inspect draft.txt

# 单文件默认写 draft.cleaned.txt
aiwr clean draft.txt

# 目录默认写 ./drafts.cleaned/，结构和名字保持不变
aiwr clean ./drafts

# 目录显式指定目标；目录目标不能放在输入目录内部
aiwr clean ./drafts --output-dir ./out

# 原地清理，第一次会创建 draft.txt.bak，已有备份不会覆盖
aiwr clean draft.txt --in-place

# 管道模式
printf 'hello\\u200bworld\\n' | aiwr clean-text -
```

选项可以放在路径前或路径后。默认只清理确定性的 Layer A 和文件元数据，不会未经请求把内容上传到模型服务。

需要让 AI agent 代用户操作时，先看 [AI agent 操作手册](docs/AI_AGENT_GUIDE.md)；
仓库根目录的 [AGENTS.md](AGENTS.md) 还提供了最小安全协议。

## 发行版安装和打包

发布版本会提供 Linux 原生包和 Windows/macOS/Linux 压缩包。Debian/Ubuntu、
Fedora/RHEL、Arch/Manjaro、Alpine 的安装命令、离线构建 recipe 和发布流程
见 [packaging/README.md](packaging/README.md)。

本地可按发行版构建：

    make package-deb       # Debian/Ubuntu
    make package-rpm       # Fedora/RHEL
    make package-apk       # Alpine
    make package-arch      # Arch/Manjaro
    make package-all       # 上述四种格式

## 命令

| 命令 | 作用 |
| --- | --- |
| `clean` / `clean-file` | 自动识别并清理文件或目录 |
| `inspect` / `inspect-file` | 输出格式、标记、命中位置和清理后风险提示 |
| `detect` | 只输出检测摘要，适合脚本和流水线 |
| `audit` / `audit-dir` | 扫描文件或目录；支持 `--json` 和 `--sarif` |
| `clean-text` / `inspect-text` | 强制文本 Layer A；无路径时使用 stdin |
| `clean-image` / `inspect-image` | 强制图片管线 |
| `clean-audio` / `clean-video` | 强制音视频管线；`--remix-audio` 为破坏性可选处理 |
| `score-stylometry` | 输出本地启发式文体统计；短文本只报告样本不足 |
| `detect-gumbel` | 使用同 key、同 tokenizer 的 HMAC/EXP Gumbel 复核 |
| `rewrite-text` | Layer B 提示词、Ollama 或 OpenAI-compatible 重写 |
| `audit-website` | 读取同源 sitemap 并审计远程公共 URL；支持 JSON/SARIF |
| `check-staged` | 检查暂存区/任意文件，不修改输入 |
| `clean-staged` | 使用 Go 核心原地清理文件，修改时返回 1 |
| `hook-written-file` | 读取 PostToolUse JSON，检查或清理刚写入的文件 |
| `stealer query|build|detect` | 无模型依赖的黑盒水印研究流水线；query 默认 dry-run |
| `download-prompts` | 从 Hugging Face datasets-server 下载可恢复的 JSONL prompt corpus |
| `score-synthid` / `synthid-score-server` / `synthid-text-server` 等研究命令 | 默认使用仓库内置 `service/scripts`；也可通过 `--upstream-scripts` 指定其它上游脚本目录 |
| `serve` | 启动 HTTP 服务，默认 `127.0.0.1:8765` |

常用选项：

```text
-o, --output FILE             单文件目标
    --output-dir DIR          目录/多文件目标
    --in-place                 原地写入并保留 .bak
    --json                     JSON 输出
-q, --quiet                   静默常规状态信息
    --only-changed             只为有变化的单文件创建目标
    --nfkc                     Unicode NFKC 规范化
    --aggressive-homoglyphs    处理选定的 Cyrillic/fullwidth 同形字
    --no-normalize-spaces      保留 NBSP、窄空格等版式字符
    --strip-bidi               连同有效嵌套一起移除 Bidi 控制符
    --strip-emoji-glue         移除 ZWJ/variation selector
    --keep-non-ai-metadata     只移除强 AI/C2PA 迹象，保留普通元数据
    --as TYPE                  auto|text|image|container|av
    --force-text               明确允许二进制样式输入走文本管线
    --deep-images MODE         auto|always|lossless|never
    --reflink MODE             auto|always|never
    --jobs N                   批处理并发提示
    --stats                    输出文本统计（clean-text 为清理计数）
    --explain                  展开 stylometry 命中短语
    --threshold N              stylometry suspicion threshold，默认 0.65
```

未知格式在自动清理时拒绝写出，避免把二进制误当 UTF-8 破坏；检查命令会报告 `unknown`。需要明确使用 `--as text` 或 `--force-text`。

## 目录输出规则

对于输入 `/data/photos`，默认目标是 `/data/photos.cleaned`：

```text
/data/photos/a.png       -> /data/photos.cleaned/a.png
/data/photos/raw/x.txt   -> /data/photos.cleaned/raw/x.txt
/data/photos/link        -> /data/photos.cleaned/link
```

普通文件的权限位会复制；目录权限也会复制；符号链接默认不跟随，而是在目标树中重建相同链接。输出目录不能等于输入目录，也不能位于输入目录内部。未知文件默认原样复制以保持目录完整性，`--skip-unknown` 可跳过它们。

`--reflink auto`（默认）在 Linux 上尝试 `FICLONE`，不支持的文件系统自动回退普通复制；`always` 失败即报错；`never` 禁用。只有输入和清理后字节完全相同的文件才走复制路径，发生真实变化时使用同目录临时文件加原子替换。

## Layer B：统计文本水印

统计水印分布在 token 选择中，不能靠删除一个字符解决。`rewrite-text` 默认不联网：

```bash
# 只打印可审阅的提示词
aiwr rewrite-text draft.txt --prompt-only

# 本地 Ollama
AIWR_REWRITE_PROVIDER=ollama AIWR_REWRITE_MODEL=llama3.2 \
  aiwr rewrite-text draft.txt -o draft.rewritten.txt

# OpenAI-compatible endpoint（密钥建议使用环境变量）
OPENAI_API_KEY=... AIWR_REWRITE_PROVIDER=openai-compatible \
  aiwr rewrite-text draft.txt --model gpt-4o-mini --allow-remote -o draft.rewritten.txt
```

它会先做 Layer A，调用一次模型，再做 Layer A。`--provider none` 表示只做 Layer A；`--in-place` 会先创建 `.bak`。重写会改变措辞、风格和可能的精度，结果只是 best-effort，绝不等同于“证明人类创作”或保证任何官方检测器失败。完整参数见 [docs/USER_GUIDE.md](docs/USER_GUIDE.md)。

同 key Gumbel 复核只在你拥有生成端的 key、tokenizer 和 PRF 布局时有意义：

```bash
aiwr score-stylometry draft.txt --json
aiwr detect-gumbel draft.txt --key 'local-secret' --json
# 也可以通过 WATERMARKS_GUMBEL_KEY 提供 key；输入 token ID 时使用 --tokens
```

## 研究流水线、网站审计和 hooks

黑盒研究流水线的 `query` 默认只生成确定性的 dry-run 回复，因此不会意外联网；`build` 从回复 JSONL 生成 `s-star`，`detect` 对候选文本评分：

```bash
aiwr stealer query --prompts prompts.jsonl --out replies.jsonl --backend dry-run
aiwr stealer build --replies replies.jsonl --baseline baseline.jsonl --out s-star.json
aiwr stealer detect --file candidate.txt --s-star s-star.json
```

要调用 OpenAI-compatible endpoint，需显式 `--allow-remote`（loopback 默认允许），并提供 API key/model。`download-prompts` 使用 datasets-server 的分页接口，写入 `prompts.jsonl` 和 `.download-state.json`；按页提交，Ctrl-C 后可继续，不会重复已提交完整页。

网站审计是 Go 原生实现：

```bash
aiwr audit-website --sitemap https://example.com/sitemap.xml --format json > website.json
aiwr audit-website --base https://example.com --sarif > website.sarif
```

它只接受 HTTP(S) 公共地址，拒绝 URL 凭据、私网/环回解析结果、跨源 sitemap 和超过 5 次的重定向；单个下载失败返回 3，发现可操作信号返回 1。网站审计不会调用本机 `c2patool`/`exiftool`。

暂存区命令和 PostToolUse hook 也直接使用 Go 核心：

```bash
aiwr check-staged --check-stylometry README.md docs/USER_GUIDE.md
aiwr clean-staged README.md docs/USER_GUIDE.md   # 改过文件返回 1，需重新 git add
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"README.md"}}' \\
  | aiwr hook-written-file --mode check
```

Hook 默认只报告；`--mode clean` 或 `WATERMARKS_HOOK_MODE=clean` 会将清理结果原子替换回原路径，不创建 `.bak`，无关工具调用保持静默。

`score-synthid`、`synthid-score-server`、`synthid-text-server`、`detect-text-watermark`、`markdiffusion`、`clean-ctrlregen` 和 `bench-synthid-text` 需要上游的 Python/模型依赖，因此仍是外部适配器。源码 checkout 会优先使用仓库内置的 `service/scripts`；也可以显式指定其它上游脚本目录：

```bash
aiwr score-synthid --upstream-scripts /path/to/watermarks-remover/service/scripts \\
  --synthid-dir /path/to/reverse-SynthID image.png

# 启动上游的 SynthID HTTP sidecar（默认 127.0.0.1:8766）
aiwr synthid-score-server --upstream-scripts /path/to/watermarks-remover/service/scripts

# 启动上游的 SynthID 文本 watermark sidecar（默认 127.0.0.1:8767）
aiwr synthid-text-server --upstream-scripts /path/to/watermarks-remover/service/scripts
```

适配器不会下载代码或权重；未安装 Python/模型依赖时会明确报错。安装后的独立二进制若不带 `service/scripts`，请使用 `--upstream-scripts` 或 `AIWR_UPSTREAM_SCRIPTS`。

## HTTP API

```bash
aiwr serve
curl http://127.0.0.1:8765/health
```

请求体使用 base64；文本水印接口也可直接传 `text`：

```bash
DATA=$(base64 -w0 < notes.md)
curl -sS -X POST http://127.0.0.1:8765/inspect \
  -H 'Content-Type: application/json' \
  -d "{\"file\":\"$DATA\",\"name\":\"notes.md\"}"

curl -sS -X POST http://127.0.0.1:8765/clean \
  -H 'Content-Type: application/json' \
  -d "{\"file\":\"$DATA\",\"name\":\"notes.md\",\"options\":{\"normalize_spaces\":false,\"layer_a_only\":true}}"

curl -sS -X POST http://127.0.0.1:8765/watermark \
  -H 'Content-Type: application/json' \
  -d '{"text":"A prompt to watermark","options":{"scheme":"synthid"}}'
```

接口与上游同名：批量请求是 `{"files":[...]}`，默认每次最多 50 个文件，可由 `WATERMARKS_MAX_BATCH_FILES` 调整。`/clean` 返回 `cleaned` base64、`report` 和兼容性附加的 `result`；`/watermark` 和 `/watermark/batch` 通过 `WATERMARKS_SYNTHID_TEXT_URL` 调用文本 sidecar，或通过 `MARKLLM_DIR` 使用显式本地适配器。未配置生成器时返回 503，sidecar/模型失败返回 502。设置 `WATERMARKS_SERVER_API_KEY`（或 `WATERMARKS_API_KEY`）后，所有 endpoint（包括 `/health`）都需要 `Authorization: Bearer ...`。

CLI 的 `clean` 默认只做确定性的 Layer A/文件清理；HTTP 的文本 `/clean` 遵循上游默认 strategy `paraphrase@0.8,mlm@0.2`，没有配置 Layer B backend/model 时会明确返回 `400`。若只需要内存中的 Layer A，像上面的请求一样显式传 `options.layer_a_only=true`。Ollama 使用 loopback 默认允许；OpenAI-compatible 或其它远端 endpoint 必须同时设置 `allow_remote` 或 `WATERMARKS_REWRITE_ALLOW_REMOTE=1`。

服务默认只绑定 loopback；暴露到局域网前请设置鉴权、限制反向代理和请求体大小。大输入上限由 `WATERMARKS_MAX_INPUT_BYTES` 控制，默认 256 MiB；文本 stdin 入口另受 `WATERMARKS_MAX_STDIN_BYTES` 控制，默认 64 MiB。
配置 `WATERMARKS_SYNTHID_SCORER_URL` 后，`/detect` 和 `detect_before`/`detect_after` 会向该 sidecar 发送图片 base64；可用 `WATERMARKS_SYNTHID_SCORER_API_KEY` 鉴权，sidecar 失败会以 `available:false` 返回。

## 支持边界和残留风险

清除 C2PA/XMP/属性不等于清除所有信号：软绑定 provenance、像素域 SynthID、音频/视频波形水印、模型训练后门以及未知厂商私有格式可能保留。PDF 会先走内置 fallback，并在 PATH 中存在时调用 qpdf、Ghostscript、ExifTool；没有这些工具时会报告降级。FLAC 原生 Vorbis Comments 和波形水印不在当前核心清理范围。

请只处理自己拥有或获授权的内容，并遵守当地法规、平台政策和署名/披露义务。报告区分“可验证移除”和“best-effort 重写”；项目不会把输出宣传为无法检测或人类创作证明。

## 跟随上游

上游仓库是本项目的行为基准。`.github/workflows/upstream-sync.yml` 每 30 分钟及手动触发检查 `main`，若上游提交变化，会自动创建/更新一个同步 PR，内容包含新的上游 SHA 和待审查的映射。它不会未经审查把外部代码或模型下载进生产构建；维护者按 [UPSTREAM.md](UPSTREAM.md) 的映射、上游测试和本项目测试逐项移植。

本地检查：

```bash
make upstream-check
```

## 开发

```bash
make fmt
make test
make vet
make smoke
```

贡献新格式时，请同时补充 magic/扩展名分类、inspect/clean、截断输入行为、目录集成测试、JSON 字段和用户手册。不要把可选研究依赖放入默认 Go 模块。

## 许可证和致谢

本 Go 重实现以 MIT 许可证发布；见 [LICENSE](LICENSE)。行为和格式覆盖参考上游 [watermarks-remover](https://github.com/guillaumemeyer/watermarks-remover)，上游同样采用 MIT 许可证；本项目不是上游官方发布物。第三方模型、工具和可选后端仍受其各自许可证约束。
