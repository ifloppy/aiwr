# aiwr 用户手册

如果由 AI agent 代为操作，请同时阅读 [AI agent 操作手册](AI_AGENT_GUIDE.md)；
其中规定了授权确认、JSON 判定、退出码和不覆盖原文件的默认流程。

## 1. 安全地开始

先检查，再清理，再检查输出：

```bash
aiwr inspect input.md --json > before.json || test $? -eq 1
aiwr clean input.md -o input.cleaned.md --json > clean.json
aiwr inspect input.cleaned.md --json > after.json || test $? -eq 1
```

`inspect` 的退出码为 0（没有可操作命中）、1（发现命中）、3（部分输入失败）；`clean` 为 0（完成）、2（参数/拒绝写入）、3（批处理有错误）。发现命中不是程序崩溃。

自动模式遇到无法识别的文件不会将其当成文本。文本专用命令也会拒绝 PNG、PDF、DOCX 等二进制输入；只有你确实要审计原始字节时才使用 `--force-text`。

## 2. 文件和目录

```bash
# 输出为 report.cleaned.pdf
aiwr clean report.pdf

# 显式输出，保持原文件不动
aiwr clean photo.png --output /tmp/photo.cleaned.png

# 目录递归处理
aiwr clean ./inbox
# ./inbox.cleaned 与 ./inbox 具有相同的相对文件名和子目录

# 多个文件必须指定一个目录
aiwr clean a.md b.html --output-dir ./cleaned
```

目录模式的细节：

- 目录目标默认是输入路径加 `.cleaned`，不是输入目录中的子目录。
- 支持文件按扩展名和 magic bytes 分流；未知文件默认原样复制以保持完整目录。
- `--skip-unknown` 跳过未知文件；`--only-changed` 也不会为没有变化的文件创建副本。
- 权限位和符号链接会被保留；默认不跟随符号链接，避免循环和越界写入。
- 输出路径的现有符号链接及父目录符号链接会被拒绝，避免通过链接写到目标树之外。

如果需要指定其它文件系统：

```bash
aiwr clean ./inbox --reflink always     # 不支持 CoW 就失败
aiwr clean ./inbox --reflink never      # 强制普通复制
```

## 3. 文本 Layer A

```bash
aiwr inspect-text article.txt
aiwr clean-text article.txt --nfkc --aggressive-homoglyphs --stats
cat article.txt | aiwr clean-text --strip-bidi > article.cleaned.txt
```

默认会去除确定性的不可见载体，并把常见非断行空格归一为普通空格。法语排版、CJK 版式或需要保留方向性的文本使用：

```bash
aiwr clean-text article.txt --no-normalize-spaces
```

`--aggressive-homoglyphs`、`--nfkc`、`--strip-emoji-glue` 可能改变多语言文字、符号语义或 emoji 组合，生产数据应先保存副本并检查 diff。有效的、承载语言意义的 Bidi/emoji 结构默认尽量保留；明确指定对应 flag 才扩大移除范围。

文本报告中的 `sample_offsets` 是解码后的 rune 单元偏移，不是编辑器的字节列号。无效 UTF-8 字节会原样保留。

## 4. 图片和元数据

```bash
aiwr inspect-image shot.png
aiwr clean-image shot.png -o shot.cleaned.png
aiwr inspect photo.avif --json
```

内置处理覆盖 PNG 文本/EXIF/C2PA 类 chunk、JPEG APP11/注释和 AI XMP、WebP 元数据 chunk、AVIF/HEIC ISOBMFF `jumb`/`uuid`、BMP 尾部、GIF comment/XMP 扩展及 TIFF IFD 元数据。清理后会再次扫描并在 `still_has_c2pa` / `still_has_ai_metadata` 中报告残留。

`--keep-non-ai-metadata` 适合希望保留普通相机/色彩信息的场景；默认路径更偏向 provenance hygiene。任何元数据清除都可能损失作者、日期、版权或色彩信息，先检查 `inspect`。

当前核心不承诺移除像素域水印。SynthID 检测可通过 `WATERMARKS_SYNTHID_SCORER_URL` 接入外部 HTTP scorer，并在失败时 fail-soft；CtrlRegen/MarkDiffusion 移除适配器仍是显式、可选且有质量损失提示的后端，而不是默认启用。

## 5. 文档和媒体容器

```bash
aiwr clean notes.docx
aiwr clean slides.pptx
aiwr clean book.epub
aiwr clean clip.mp4
aiwr clean recording.wav
```

DOCX/XLSX/PPTX 会处理文档属性和 `customXml`；EPUB 会处理 OPF/XHTML 及可识别的嵌入媒体；ODT 处理 `meta.xml`；HTML 处理 AI-like meta、JSON-LD 和 `data-ai*`；Markdown 处理 YAML frontmatter 并继续执行 Layer A；MP4/MOV/M4A/M4V 处理 ISOBMFF provenance 和 `moov/udta`；WAV 处理 RIFF metadata；MP3/FLAC 处理相应 ID3 载体。

PDF 先由纯 Go 核心执行保守 fallback；若 PATH 中已有 `qpdf`、Ghostscript 或 `exiftool`，aiwr 会按 `deep_images` 策略自动尝试结构重写、元数据清理和必要的渲染重写。程序不会安装或下载这些工具。清理动作出现 warning 时，不要把“已写出”理解成“所有 PDF 对象都不可恢复”。

音频破坏性重混：

```bash
aiwr clean-audio speech.wav --remix-audio --audio-tempo 1.08 --audio-pitch 2
```

这需要 PATH 中的 `ffmpeg`，会改变编码、时长/音高特征和音质；它不是元数据清除的默认步骤。音视频波形水印没有通用、无损的清理保证。

## 6. 文体统计和同 key Gumbel 复核

这些命令是检测/审计，不会修改输入：

```bash
aiwr score-stylometry article.txt --json
aiwr detect-gumbel article.txt --key 'local-secret' --json
aiwr score-stylometry article.txt --explain
```

`--explain` 会在可读输出中展开命中的模板短语；JSON 输出始终保留结构化命中信息。

文体统计是启发式信号，少于 30 个词时只报告样本不足。同 key Gumbel 复核只有在 key、tokenizer 和生成端 PRF 布局完全一致时才有解释力；没有 key 时不要把 unavailable 当成“未检测到”。

## 7. Layer B 重写

统计型 token 水印不能从文件字节中确定性删除。`rewrite-text` 只在显式指定 provider 或环境变量时调用模型：

```bash
aiwr rewrite-text draft.txt --prompt-only

AIWR_REWRITE_PROVIDER=ollama \
AIWR_REWRITE_MODEL=llama3.2 \
aiwr rewrite-text draft.txt -o draft.rewritten.txt

AIWR_REWRITE_PROVIDER=openai-compatible \
AIWR_REWRITE_BASE_URL=https://example.invalid/v1/chat/completions \
OPENAI_API_KEY=... \
aiwr rewrite-text draft.txt --model my-model --allow-remote -o draft.rewritten.txt
```

支持 `none`、`ollama`、`openai`、`openai-compatible`。默认 provider 是 `print-prompt`，所以 CI 不会隐式外传文本。`--prompt-only`/`--dry-run` 只打印完整 prompt；`--no-layer-a` 跳过确定性前后处理。密钥不要放命令历史，优先环境变量。

模型 endpoint 的网络安全：只向你明确配置的 endpoint 发送数据；远程服务会接触原文，须自行确认隐私和合规。重写后必须人工复核事实、数字、代码、引用和格式。项目不能检测所有厂商的 secret-key 水印，也不能保证输出“不可检测”。

## 8. 黑盒水印研究流水线

这组命令对应上游 `stealer/`，其中 scorer 是无模型的 Go 实现；它用于研究和复核水印分布，不会从文本中恢复 secret key。

```bash
# 离线生成可重复的占位回复
aiwr stealer query --prompts prompts.jsonl --out replies.jsonl --backend dry-run

# 从水印回复（可选 baseline）估计 s* 表
aiwr stealer build --replies replies.jsonl \\
  --baseline baseline.jsonl --ctx 8 --topk 50 --out s-star.json

# 对候选文本评分；也可使用 --text '...'
aiwr stealer detect --file candidate.txt --s-star s-star.json
```

`query` 支持 JSONL（默认读取 `text` 字段）或每行一个 prompt，并按输入顺序写出 `prompt/reply` JSONL。默认 `dry-run` 不联网；`openai-compatible` 需要 `--model`、`--api-key` 和 endpoint，loopback 默认允许，非 loopback 必须显式 `--allow-remote`。并发请求不会改变输出顺序。

下载 prompt corpus：

```bash
aiwr download-prompts --dataset allenai/c4 --config realnewslike \\
  --split train --count 30000 --out ./stealer/prompts
```

程序使用 Hugging Face datasets-server 的 `/rows` 分页接口，读取 `HF_TOKEN`（只放在请求头，不写入日志），按页提交 `prompts.jsonl`，并把 `next_offset/written` 保存到 `.download-state.json`。默认延迟 1.5 秒；`--min-chars`、`--max-chars`、`--offset` 和 `--start-over` 控制筛选/恢复。Ctrl-C 返回 130，并回滚到最近一个完整页；网络/服务端错误会重试有限次数，不能将返回 2（数据集提前耗尽）当成成功。

## 9. 网站审计

```bash
aiwr audit-website --sitemap https://example.com/sitemap.xml --format json > website.json
aiwr audit-website --base https://example.com --max-pages 200 --sarif > website.sarif
```

`--sitemap` 和 `--base` 二选一；后者按 `/sitemap.xml`、`/sitemap_index.xml`、`robots.txt` 顺序发现。审计支持 `--format human|json|sarif`，`--json`/`--sarif` 是快捷方式，`--max-bytes` 默认 4 MiB，`--timeout` 默认 15 秒，`--max-pages` 默认 200，`--check-stylometry` 可加入启发式文体信号。

这是有意收紧的远程读取器：只接受 HTTP(S)，拒绝 URL 用户名/密码、非公共 DNS/IP、跨源 sitemap、DNS 解析到私网的目标和超过 5 次重定向；sitemap 解压/解析上限为 64 MiB，并拒绝 DTD/ENTITY。远程资产只用 Go 核心扫描，不执行本机可选工具。退出码为 0（完整且无可操作发现）、1（完整但有可操作发现）、2（参数或 sitemap 失败）、3（部分 URL 下载/检查失败）。

## 10. 暂存区和写入 Hook

```bash
# 只检查，不修改；适合 pre-commit
aiwr check-staged file1.md file2.png
aiwr check-staged --check-stylometry file1.md

# 原地清理；第一次为每个成功处理的文件创建 .bak
aiwr clean-staged file1.md file2.png
```

`check-staged` 对可操作 C2PA/AI/Layer A 发现返回 1，对参数或不可读文件返回 2；未知格式和超过 256 MiB 的文件跳过。`clean-staged` 使用统一 Go 核心，未知/超大文件跳过，至少一个文件发生变化返回 1（提醒重新暂存），处理失败返回 3，已干净返回 0。原有 `.bak` 不会覆盖。

PostToolUse hook 从 stdin 接收 Claude 风格 JSON：

```bash
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"docs/note.md"}}' \\
  | aiwr hook-written-file --mode check
printf '%s\n' '{"tool_name":"Edit","cwd":".","tool_input":{"file_path":"docs/note.md"}}' \\
  | WATERMARKS_HOOK_MODE=clean aiwr hook-written-file
```

只处理 `Write`、`Edit`、`MultiEdit`、`NotebookEdit`、`Update` 的 `file_path`/`notebook_path`；其它 payload 静默退出。`check` 发现信号时输出 PostToolUse JSON 并返回 2；`clean` 只在字节变化时原子替换、保留权限、不创建 `.bak`，Hook 本身不会阻塞已经完成的写入。路径为符号链接或清理失败时保持原文件并报告 hook error。

## 11. 可选研究/重型后端

以下命令保留上游入口，但模型、权重或第三方许可证不进入默认 Go 二进制：

| 命令 | 外部脚本 | 需要 |
| --- | --- | --- |
| `score-synthid` | `score_synthid.py` | reverse-SynthID checkout；`--synthid-dir` 会映射为上游 `--upstream-dir` |
| `synthid-score-server` | `synthid_score_server.py` | reverse-SynthID sidecar；`--host`、`--port`、`--api-key` 原样透传 |
| `synthid-text-server` | `synthid_text_server.py` | SynthID 文本生成 sidecar；需显式上游脚本和模型依赖 |
| `detect-text-watermark` | `detect_text_watermark.py` | MarkLLM checkout；`--markllm-dir` 会映射为上游目录参数 |
| `markdiffusion` | `markdiffusion_harness.py` | MarkDiffusion checkout、模型和设备 |
| `clean-ctrlregen` | `clean_ctrlregen.py` | CtrlRegen/noai-watermark checkout、模型和设备 |
| `bench-synthid-text` | `bench_synthid_text.py` | 上游 benchmark 依赖 |

源码 checkout 默认会使用内置的 `service/scripts`；需要其它版本时可通过 `--upstream-scripts PATH` 或 `AIWR_UPSTREAM_SCRIPTS=PATH` 指定，例如：

```bash
aiwr detect-text-watermark --upstream-scripts /src/watermarks-remover/service/scripts \\
  --markllm-dir /src/MarkLLM --text candidate.txt
```

适配器使用受限的子进程和临时文件边界，不会自动下载源码/权重；未配置时返回清晰的 `available:false/error` 报告。图片元数据清理仍会落地，像素后端不可用时命令返回 1；`rewrite-text --tactic mlm`、SynthID local scorer、CtrlRegen/MarkDiffusion 像素清理也遵循同一显式外部后端原则；可先看 `aiwr ... --help` 和输出中的 `available/error`。

## 12. HTTP 服务

```bash
WATERMARKS_SERVER_API_KEY=change-me aiwr serve --host 127.0.0.1 --port 8765
```

`WATERMARKS_SERVER_HOST` 与 `WATERMARKS_SERVER_PORT` 可设置监听默认值，命令行参数优先。

健康检查：

```bash
curl -sS -H 'Authorization: Bearer change-me' http://127.0.0.1:8765/health
curl -sS -H 'Authorization: Bearer change-me' http://127.0.0.1:8765/capabilities
curl -sS -H 'Authorization: Bearer change-me' http://127.0.0.1:8765/openapi.json
```

单文件 JSON：

```json
{
  "file": "<base64>",
  "name": "notes.md",
  "options": {
    "normalize_spaces": false,
    "nfkc": false,
    "aggressive_homoglyphs": false,
    "keep_non_ai_metadata": false,
    "layer_a_only": true,
    "as": "auto"
  }
}
```

`/inspect` 返回 `kind`、结构化的 `suspicious` evidence 和 `report`；`/detect` 还返回 `detections`，包括可用性和 scheme-specific 结果；配置 SynthID HTTP sidecar 后，图片检测会将其 score 一并返回，`WATERMARKS_SYNTHID_SCORER_API_KEY` 用于 sidecar Bearer 鉴权。`/clean` 返回 `cleaned` base64、清理后 `report` 和 `result`。`/watermark` 接受 `text` 或 base64 `file`，以及可选 `keys`/`options`，通过 `WATERMARKS_SYNTHID_TEXT_URL` 调用 sidecar；也可设置 `MARKLLM_DIR` 使用显式本地适配器。批量使用 `files` 数组，单个坏条目只在该条目返回 `ok:false`，不会丢弃其它结果。当前有 `/inspect/batch`、`/detect/batch`、`/clean/batch`、`/watermark/batch` 四个批量接口，默认上限为 50；文本生成未配置时为 503，后端失败为 502。

CLI 的 `clean` 只做 Layer A/文件清理；服务端文本 `/clean` 默认执行上游 strategy `paraphrase@0.8,mlm@0.2`。没有配置 Layer B backend/model 时，文本清理会返回 400；需要确定性清理时显式传 `layer_a_only: true`。服务端远程 rewrite endpoint 还需要 `allow_remote: true` 或环境变量 `WATERMARKS_REWRITE_ALLOW_REMOTE=1`。

服务没有上传文件到磁盘，清理接口使用内存字节；输入上限默认 256 MiB。若要在不可信多租户环境使用，请在反向代理设置更小的 body/timeout 上限，并将服务运行在独立用户和容器中。

## 13. CI / SARIF / 更新

```bash
aiwr audit . --sarif > aiwr.sarif
aiwr audit ./docs --json
make upstream-check
```

`.github/workflows/go.yml` 执行格式化、测试、vet、race；`.github/workflows/upstream-sync.yml` 定时读取上游 `main`，变化时开同步 PR。同步 PR 只改变追踪指针和审查记录，真正的行为移植仍需通过测试和代码审查。

## 14. 故障排查

| 现象 | 处理 |
| --- | --- |
| `unknown format` | 传入正确扩展名，或明确 `--as text|image|container|av`；不要盲目 `--force-text` |
| `refusing ... binary` | 文本命令收到二进制；改用统一 `clean`，原始字节审计才用 `--force-text` |
| `output directory ... inside input` | 把输出移到输入目录旁边或更高层 |
| reflink 失败 | `--reflink auto` 会回退；只有 `always` 才会报错 |
| PDF 有 warning | 检查 qpdf/Ghostscript/exiftool 是否在 PATH；保留原文并对需要的 PDF 用外部工具复核 |
| remix 失败 | 检查 `ffmpeg`、codec、临时目录空间和 timeout |
| HTTP 401 | 检查 `WATERMARKS_SERVER_API_KEY` 与 Bearer token 是否完全一致 |
| Layer B 无输出 | 使用 `--prompt-only`，或配置 Ollama/OpenAI-compatible provider/model |
