# aiwr 用户手册

English version: [USER_GUIDE.md](USER_GUIDE.md)。如果由 AI agent 代用户操作，还要阅读 [AI_AGENT_GUIDE.md](AI_AGENT_GUIDE.md)。

## 1. 先检查，再清理，再检查

默认写到新目标：

```bash
aiwr inspect input.md --json > before.json || test $? -eq 1
aiwr clean input.md --output input.cleaned.md --json > clean.json
aiwr inspect input.cleaned.md --json > after.json || test $? -eq 1
```

退出码 1 表示发现命中或残留，不代表程序崩溃；2 表示参数错误、拒绝处理或单文件不支持；3 表示批处理存在失败。脚本决策优先使用 JSON。

文件默认生成旁边的 `NAME.cleaned.EXT`，目录默认生成相邻的 `DIRECTORY.cleaned`。多个文件必须使用 `--output-dir`。目录会保留相对路径、权限和符号链接，目标不能等于输入或位于输入内部。目录中的未知文件默认原样复制；`--skip-unknown` 会跳过它们。

只有明确需要修改原文件时才使用 `--in-place`；它第一次会创建 `FILE.bak`，不会覆盖已有备份，目录不支持该选项。

## 2. 语言选择

CLI 默认英语。`AIWR_LANG`/`AIWR_LANGUAGE`、`LC_ALL`、`LC_MESSAGES`、`LANGUAGE` 或 `LANG` 的值以 `zh` 开头时使用中文，优先级按此顺序；不支持或缺失时回退英语：

```bash
AIWR_LANG=zh-CN aiwr inspect --help
LANG=en_US.UTF-8 aiwr inspect --help
```

帮助、状态、进度和诊断会本地化；JSON/SARIF 不翻译，以保持字段稳定。外部上游 Python 适配器的帮助仍由上游命令输出。

## 3. 文本 Layer A

```bash
aiwr inspect-text article.txt
aiwr clean-text article.txt --nfkc --aggressive-homoglyphs --stats
cat article.txt | aiwr clean-text --strip-bidi -
```

默认移除确定性的不可见携带物并归一常见异体空格。`--no-normalize-spaces` 可保留空格样式。`--nfkc`、`--aggressive-homoglyphs`、`--strip-bidi`、`--strip-emoji-glue` 可能改变多语言文本、布局或 emoji 组合，发布前请检查 diff。

文本 `sample_offsets` 是解码后的 rune 偏移，不是字节列号；无效 UTF-8 字节会保留。文本命令拒绝疑似二进制输入，只有明确要把原始字节作为文本时才使用 `--force-text`。

## 4. 文件、容器和媒体

```bash
aiwr inspect-image shot.png
aiwr clean-image shot.png --output shot.cleaned.png
aiwr clean notes.docx
aiwr clean slides.pptx
aiwr clean book.epub
aiwr clean clip.mp4
```

内置管线覆盖常见图片元数据、SVG/PDF/OOXML/ODT/EPUB/HTML/Markdown 容器和 MP4/MOV/M4A/M4V/WAV/MP3/FLAC 元数据。若 PATH 中已有 `qpdf`、Ghostscript 或 ExifTool，PDF 可能使用它们；aiwr 不会安装或下载这些工具。

`--keep-non-ai-metadata` 尽量保留普通元数据。清理可能损失作者、日期、版权或色彩信息；核心不保证无损移除像素、波形、私有或密钥型水印。音频重混需要 `ffmpeg` 且有损：

```bash
aiwr clean-audio speech.wav --remix-audio --audio-tempo 1.08 --audio-pitch 2
```

## 5. Layer B 重写和检测

token 采样型水印通常不能靠删除字符移除。`rewrite-text` 默认离线：

```bash
aiwr rewrite-text draft.txt --prompt-only

AIWR_REWRITE_PROVIDER=ollama AIWR_REWRITE_MODEL=llama3.2 \
  aiwr rewrite-text draft.txt --output draft.rewritten.txt

AIWR_REWRITE_PROVIDER=openai-compatible OPENAI_API_KEY=... \
  aiwr rewrite-text draft.txt --allow-remote --output draft.rewritten.txt
```

远程 endpoint 会接触原文，使用 `--allow-remote` 前确认隐私与授权。重写后检查事实、数字、代码、引用和格式；结果是 best-effort，不能证明人类创作，也不保证检测器结果。

本地文体统计和同 key Gumbel 检测不会修改输入：

```bash
aiwr score-stylometry article.txt --json
aiwr detect-gumbel article.txt --key local-secret --json
```

Gumbel 检测只有在 key、tokenizer 和生成端 PRF 布局一致时才有意义。

## 6. 研究流水线和外部适配器

```bash
aiwr stealer query --prompts prompts.jsonl --out replies.jsonl --backend dry-run
aiwr stealer build --replies replies.jsonl --out s-star.json
aiwr stealer detect --file candidate.txt --s-star s-star.json
aiwr download-prompts --dataset allenai/c4 --config realnewslike \
  --split train --count 30000 --out ./stealer/prompts
```

`stealer query` 默认 dry-run 且离线；远程模型需要显式 backend、凭据和 `--allow-remote`。prompt 下载器使用可恢复的分页 checkpoint；Ctrl-C 返回 130 并保留最近完整页。

`score-synthid`、`synthid-score-server`、`synthid-text-server`、`detect-text-watermark`、`markdiffusion`、`clean-ctrlregen` 和 `bench-synthid-text` 是兼容适配器。源码 checkout 默认使用内置 `service/scripts`，也可用 `--upstream-scripts PATH` 或 `AIWR_UPSTREAM_SCRIPTS` 指定其它目录；适配器不会自动下载代码或权重。

## 7. 网站审计

只有在获得明确授权后访问网站：

```bash
aiwr audit-website --sitemap https://example.com/sitemap.xml --format json
aiwr audit-website --base https://example.com --sarif
```

它接受公共 HTTP(S) URL，拒绝凭据、私网目标、跨源 sitemap、恶意 XML 实体和过多重定向；远程资源使用 Go 核心扫描，不执行本机可选工具。

## 8. Hook、暂存区和 HTTP

```bash
aiwr check-staged file1.md file2.png
aiwr clean-staged file1.md file2.png
printf '%s\n' '{"tool_name":"Write","cwd":".","tool_input":{"file_path":"note.md"}}' \
  | aiwr hook-written-file --mode check
```

`check-staged` 不修改文件；`clean-staged` 原地修改后需要重新暂存。Hook 默认检查；clean 模式只在字节变化时替换，不创建备份。

启动 HTTP 服务：

```bash
WATERMARKS_SERVER_API_KEY=change-me aiwr serve
curl -H 'Authorization: Bearer change-me' \
  http://127.0.0.1:8765/capabilities
```

服务提供 `/health`、`/capabilities`、`/openapi.json`、`/inspect`、`/detect`、`/clean`、`/watermark` 和批量接口。文件使用 base64，`/watermark` 也接受文本；默认只监听 loopback，对外提供前请配置鉴权。

## 9. 开发和限制

```bash
make format
make test
make vet
make smoke
make upstream-check
```

接受上游变更前请阅读 [UPSTREAM.md](../UPSTREAM.md)。不要将可选研究依赖加入默认 Go module；agent 使用的授权、JSON、退出码和失败处理契约见 [AI_AGENT_GUIDE.md](AI_AGENT_GUIDE.md)。
