package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// cliLanguage controls human-readable CLI text. Machine-readable output is
// deliberately kept stable and is not passed through cliText.
type cliLanguage uint8

const (
	cliEnglish cliLanguage = iota
	cliChinese
)

// detectCLILanguage follows the usual locale precedence and also provides an
// explicit per-command override for scripts and users who do not want to
// change their process locale. Unknown or malformed values intentionally use
// English as the fallback language.
func detectCLILanguage() cliLanguage {
	for _, name := range []string{"AIWR_LANG", "AIWR_LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		value = strings.ToLower(value)
		if strings.HasPrefix(value, "zh") || strings.HasPrefix(value, "chinese") {
			return cliChinese
		}
		return cliEnglish
	}
	return cliEnglish
}

func cliText(english, chinese string) string {
	if detectCLILanguage() == cliChinese && strings.TrimSpace(chinese) != "" {
		return chinese
	}
	return english
}

func localizedUsage() string { return cliText(usageEnglish, usageChinese) }

func localizedStealerUsage() string { return cliText(stealerUsageEnglish, stealerUsageChinese) }

func localizedFlagDescription(english string) string {
	if detectCLILanguage() != cliChinese {
		return english
	}
	translations := map[string]string{
		"allow binary-looking input through the text pipeline":             "允许疑似二进制输入通过文本管线",
		"allow binary-looking text input":                                  "允许疑似二进制文本输入",
		"allow non-loopback model endpoints":                               "允许非 loopback 模型端点",
		"allow non-loopback rewrite endpoints":                             "允许非 loopback 重写端点",
		"alias for --aggressive-homoglyphs":                                "--aggressive-homoglyphs 的别名",
		"alias for --audio-tempo":                                          "--audio-tempo 的别名",
		"alias for --audio-pitch":                                          "--audio-pitch 的别名",
		"alias for --audio-bitrate":                                        "--audio-bitrate 的别名",
		"alias for --jobs":                                                 "--jobs 的别名",
		"alias for --output":                                               "--output 的别名",
		"alias for --prompt-only":                                          "--prompt-only 的别名",
		"alias for --provider; also accepts print-prompt":                  "--provider 的别名；也接受 print-prompt",
		"alias for --remix-audio":                                          "--remix-audio 的别名",
		"alias for --upstream-scripts":                                     "--upstream-scripts 的别名",
		"apply optional ffmpeg audio remix":                                "应用可选的 ffmpeg 音频重混",
		"apply Unicode NFKC normalization":                                 "应用 Unicode NFKC 规范化",
		"apply Unicode NFKC normalization in Layer A":                      "在 Layer A 中应用 Unicode NFKC 规范化",
		"audio bitrate for destructive remix":                              "有损重混使用的音频码率",
		"audio output codec override":                                      "覆盖音频输出编码器",
		"audit output: human, json, or sarif":                              "审计输出：human、json 或 sarif",
		"audit rather than clean where supported":                          "在支持的位置执行审计而非清理",
		"Bearer API key (prefer an environment variable)":                  "Bearer API key（优先使用环境变量）",
		"candidate selection: min-divergence or max-margin":                "候选选择：min-divergence 或 max-margin",
		"comma-separated directory names to skip during audit":             "审计时跳过的目录名，使用逗号分隔",
		"context window size":                                              "上下文窗口大小",
		"CtrlRegen diffusion steps":                                        "CtrlRegen 扩散步数",
		"CtrlRegen device: auto|cpu|cuda|mps":                              "CtrlRegen 设备：auto|cpu|cuda|mps",
		"dataset config/subset":                                            "数据集配置/子集",
		"dataset id":                                                       "数据集 ID",
		"datasets-server base URL":                                         "datasets-server 基础 URL",
		"dataset split":                                                    "数据集 split",
		"default: stdout for stdin, NAME.rewritten.EXT for a file":         "默认：stdin 输出到 stdout，文件输出为 NAME.rewritten.EXT",
		"DiffusionPurification device: auto|cpu|cuda|mps":                  "DiffusionPurification 设备：auto|cpu|cuda|mps",
		"DiffusionPurification steps":                                      "DiffusionPurification 步数",
		"DiffusionPurification working size":                               "DiffusionPurification 工作尺寸",
		"do not scrub visible text bodies inside HTML/office containers":   "不要清理 HTML/office 容器中的可见文本主体",
		"drop prompts shorter than this":                                   "丢弃短于此长度的 prompt",
		"embed text cleaning statistics":                                   "包含文本清理统计",
		"emit JSON":                                                        "输出 JSON",
		"emit machine-readable JSON":                                       "输出机器可读 JSON",
		"emit SARIF 2.1.0":                                                 "输出 SARIF 2.1.0",
		"emit rewrite statistics as JSON":                                  "以 JSON 输出重写统计",
		"embedded image policy: auto, always, lossless, never":             "嵌入图片策略：auto、always、lossless、never",
		"ffmpeg pitch shift in semitones":                                  "ffmpeg 音高偏移（半音）",
		"ffmpeg tempo factor, 0.5..2.0":                                    "ffmpeg 速度因子，范围 0.5..2.0",
		"ffmpeg timeout in seconds":                                        "ffmpeg 超时秒数",
		"force type: auto, text, image, container, av":                     "强制类型：auto、text、image、container、av",
		"fraction of video frames to purify":                               "需要净化的视频帧比例",
		"Hugging Face dataset id":                                          "Hugging Face 数据集 ID",
		"include detailed stylometry marker matches":                       "包含详细文体标记命中",
		"include heuristic text stylometry":                                "包含启发式文本文体统计",
		"include text cleaning statistics":                                 "包含文本清理统计",
		"include text stylometry":                                          "包含文本文体统计",
		"input file":                                                       "输入文件",
		"JSONL of prompt/reply rows":                                       "prompt/reply 行的 JSONL 文件",
		"keyed watermark key (prefer WATERMARKS_GUMBEL_KEY)":               "水印 key（优先使用 WATERMARKS_GUMBEL_KEY）",
		"listen host":                                                      "监听主机",
		"listen port":                                                      "监听端口",
		"MarkDiffusion checkout root":                                      "MarkDiffusion checkout 根目录",
		"MarkDiffusion timeout in seconds":                                 "MarkDiffusion 超时秒数",
		"MarkLLM checkout root":                                            "MarkLLM checkout 根目录",
		"MarkLLM detection timeout in seconds":                             "MarkLLM 检测超时秒数",
		"MarkLLM scoring model":                                            "MarkLLM 评分模型",
		"MarkLLM scheme: kgw, synthid, synthid-text, exp, unigram, or sir": "MarkLLM 方案：kgw、synthid、synthid-text、exp、unigram 或 sir",
		"maximum generated tokens":                                         "最大生成 token 数",
		"maximum URLs to scan":                                             "最多扫描的 URL 数",
		"maximum bytes per downloaded asset":                               "每个下载资源的最大字节数",
		"maximum evaluation loops":                                         "最大评估循环数",
		"minimum context occurrences":                                      "上下文最少出现次数",
		"minimum detector margin below threshold":                          "低于阈值的最小检测器余量",
		"model name":                                                           "模型名称",
		"model name (or WATERMARKS_STEAL_MODEL)":                               "模型名称（或 WATERMARKS_STEAL_MODEL）",
		"normalize selected Cyrillic/fullwidth homoglyphs":                     "归一化选定的 Cyrillic/全角同形字",
		"normalize selected homoglyphs in Layer A":                             "在 Layer A 中归一化选定同形字",
		"number of prompts to keep":                                            "保留的 prompt 数量",
		"OpenAI-compatible API base URL":                                       "OpenAI-compatible API 基础 URL",
		"OpenAI-compatible chat endpoint":                                      "OpenAI-compatible chat endpoint",
		"OpenAI-compatible reasoning effort: none, low, medium, high, or off":  "OpenAI-compatible reasoning effort：none、low、medium、high 或 off",
		"optional CtrlRegen RNG seed":                                          "可选的 CtrlRegen RNG seed",
		"optional non-watermarked baseline replies (JSONL)":                    "可选的无水印基线回复（JSONL）",
		"optional upstream service/scripts directory (enables the mlm tactic)": "可选的上游 service/scripts 目录（启用 mlm tactic）",
		"output directory (default: ./stealer/prompts)":                        "输出目录（默认：./stealer/prompts）",
		"output directory for directory or multi-file input":                   "目录或多文件输入的输出目录",
		"output file": "输出文件",
		"output file (default: NAME.cleaned.EXT)":                                           "输出文件（默认：NAME.cleaned.EXT）",
		"output file (default: stdout for stdin, NAME.rewritten.EXT for a file)":            "输出文件（默认：stdin 为 stdout，文件为 NAME.rewritten.EXT）",
		"output format: human, json, or sarif":                                              "输出格式：human、json 或 sarif",
		"output s-star JSON":                                                                "输出 s-star JSON",
		"parallel model requests":                                                           "并行模型请求数",
		"parallelism hint for batch operations":                                             "批处理并行度提示",
		"p-value threshold":                                                                 "p-value 阈值",
		"prompt corpus (JSONL or plain lines)":                                              "prompt corpus（JSONL 或普通文本行）",
		"print the Layer B prompt and do not call a provider":                               "输出 Layer B prompt，不调用 provider",
		"print service version and exit":                                                    "输出服务版本并退出",
		"read candidate text from a file":                                                   "从文件读取候选文本",
		"read token IDs as JSON array or one per line":                                      "将 token ID 作为 JSON 数组或逐行读取",
		"remove ZWJ/variation selectors when explicitly requested":                          "明确请求时移除 ZWJ/variation selector",
		"remove bidi controls, including valid embeddings":                                  "移除 bidi 控制符，包括有效嵌套",
		"remove only strongly AI/provenance-like metadata":                                  "仅移除明显像 AI/来源的元数据",
		"replace files in place and create FILE.bak":                                        "原地替换文件并创建 FILE.bak",
		"replace a file after creating FILE.bak":                                            "创建 FILE.bak 后替换文件",
		"rewrite intensity in (0,1]":                                                        "重写强度，范围 (0,1]",
		"rewrite provider: none, openai, openai-compatible, ollama":                         "重写 provider：none、openai、openai-compatible、ollama",
		"rewriting instruction":                                                             "重写指令",
		"same-key Gumbel detector key (prefer WATERMARKS_GUMBEL_KEY)":                       "同 key Gumbel 检测 key（优先使用 WATERMARKS_GUMBEL_KEY）",
		"sampling temperature":                                                              "采样温度",
		"save candidate scorer JSON":                                                        "保存候选评分器 JSON",
		"saved s-star JSON":                                                                 "已保存的 s-star JSON",
		"seconds between page requests":                                                     "分页请求间隔秒数",
		"skip deterministic Layer A cleanup before/after rewriting":                         "跳过重写前后的确定性 Layer A 清理",
		"skip unknown files in directory batches":                                           "目录批处理中跳过未知文件",
		"skip ZWJ/variation selectors":                                                      "清理 emoji joiner",
		"source language":                                                                   "源语言",
		"statistical suspicion threshold":                                                   "统计可疑阈值",
		"strategy JSON file":                                                                "Layer B 默认 strategy JSON 文件",
		"strip bidi controls in Layer A":                                                    "在 Layer A 中移除 bidi 控制符",
		"strip emoji joiners in Layer A":                                                    "在 Layer A 中移除 emoji joiner",
		"style instruction":                                                                 "可选的写作风格指令",
		"target margin":                                                                     "目标余量",
		"text to score":                                                                     "要评分的文本",
		"tokens retained per context":                                                       "每个上下文保留的 token 数",
		"truncate prompts longer than this":                                                 "截断长于此长度的 prompt",
		"upstream service/scripts directory for optional Python/GPU adapters":               "可选 Python/GPU 适配器的上游 service/scripts 目录",
		"video temporal vote threshold":                                                     "视频时间投票阈值",
		"watermarked replies (JSONL)":                                                       "带水印回复（JSONL）",
		"write/report only changed files where applicable":                                  "在适用时仅写入/报告有变化的文件",
		"write the Layer B prompt and do not call a provider":                               "写出 Layer B prompt，不调用 provider",
		"sitemap URL to audit":                                                              "要审计的 sitemap URL",
		"base URL; discover sitemap automatically":                                          "基础 URL；自动发现 sitemap",
		"per-request timeout in seconds":                                                    "每次请求的超时秒数",
		"hook mode: check or clean":                                                         "hook 模式：check 或 clean",
		"API key (prefer OPENAI_API_KEY)":                                                   "API key（优先使用 OPENAI_API_KEY）",
		"API key (or WATERMARKS_STEAL_API_KEY)":                                             "API key（或 WATERMARKS_STEAL_API_KEY）",
		"query backend: dry-run or openai-compatible":                                       "查询后端：dry-run 或 openai-compatible",
		"watermark key (prefer WATERMARKS_GUMBEL_KEY)":                                      "水印 key（优先使用 WATERMARKS_GUMBEL_KEY）",
		"add-alpha smoothing":                                                               "add-alpha 平滑",
		"override context length (0 uses the scorer value)":                                 "覆盖上下文长度（0 使用评分器的值）",
		"field used as the prompt":                                                          "作为 prompt 的字段",
		"ignore resume state":                                                               "忽略恢复状态",
		"dataset row offset":                                                                "数据集行偏移量",
		"alias for -V":                                                                      "-V 的别名",
		"Layer B default strategy JSON file":                                                "Layer B 默认 strategy JSON 文件",
		"reverse-SynthID checkout root for optional pixel scoring":                          "可选像素评分的 reverse-SynthID checkout 根目录",
		"pixel remover: ctrlregen or diffusion":                                             "像素清理器：ctrlregen 或 diffusion",
		"CtrlRegen/noai-watermark checkout root":                                            "CtrlRegen/noai-watermark checkout 根目录",
		"CtrlRegen regeneration intensity in (0,1]":                                         "CtrlRegen 重生成强度，范围 (0,1]",
		"MarkDiffusion intensity in (0,1]":                                                  "MarkDiffusion 强度，范围 (0,1]",
		"Stable Diffusion model for purification":                                           "净化使用的 Stable Diffusion 模型",
		"preserve non-breaking and space-like characters":                                   "保留不换行空格和异体空格",
		"include stylometry in audit output":                                                "在审计输出中包含文体统计",
		"CtrlRegen timeout in seconds":                                                      "CtrlRegen 超时秒数",
		"DiffusionPurification intensity in (0,1]":                                          "DiffusionPurification 强度，范围 (0,1]",
		"suppress routine status output":                                                    "隐藏常规状态输出",
		"unchanged-file copy policy: auto, always, never":                                   "未变化文件复制策略：auto、always、never",
		"emit SARIF audit output":                                                           "输出 SARIF 审计结果",
		"pivot language for backtranslate":                                                  "回译使用的中间语言",
		"preserve space-like characters in Layer A":                                         "在 Layer A 中保留异体空格",
		"flag rewrites whose lexical divergence is below this floor; 0 disables":            "标记词汇差异低于此下限的重写；0 表示禁用",
		"ordered tactic@intensity steps":                                                    "按顺序排列的 tactic@intensity 步骤",
		"optional writing-style instruction":                                                "可选的写作风格指令",
		"rewrite tactic: paraphrase, backtranslate, structural, humanize, code, chunk, mlm": "重写 tactic：paraphrase、backtranslate、structural、humanize、code、chunk、mlm",
		"shuffle rewritten chunks when using the chunk tactic":                              "使用 chunk tactic 时打乱重写后的片段",
	}
	if chinese, ok := translations[english]; ok {
		return chinese
	}
	return english
}

func setLocalizedFlagUsage(fs *flag.FlagSet, name string) {
	englishUsage := make(map[string]string)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s %s:\n", cliText("Usage of", "用法："), name)
		fs.VisitAll(func(f *flag.Flag) {
			if _, ok := englishUsage[f.Name]; !ok {
				englishUsage[f.Name] = f.Usage
			}
			f.Usage = localizedFlagDescription(englishUsage[f.Name])
		})
		fs.PrintDefaults()
	}
}

func setLocalizedCommonUsage(fs *flag.FlagSet, command string) {
	englishUsage := make(map[string]string)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), localizedUsage())
		fmt.Fprintf(fs.Output(), "\n%s %s:\n", cliText("Options for", "选项："), command)
		fs.VisitAll(func(f *flag.Flag) {
			if _, ok := englishUsage[f.Name]; !ok {
				englishUsage[f.Name] = f.Usage
			}
			f.Usage = localizedFlagDescription(englishUsage[f.Name])
		})
		fs.PrintDefaults()
	}
}

// localizedProgressWriter translates the small, stable progress messages
// emitted by the stealer package while preserving paths, URLs, and errors.
// Writes from fmt.Fprintf are line-oriented in these call sites; incomplete
// fragments are forwarded unchanged.
type localizedProgressWriter struct {
	writer io.Writer
}

func (w localizedProgressWriter) Write(data []byte) (int, error) {
	if detectCLILanguage() != cliChinese {
		return w.writer.Write(data)
	}
	line := string(data)
	switch {
	case strings.HasPrefix(line, "querying "):
		line = strings.Replace(line, "querying ", "正在查询 ", 1)
		line = strings.Replace(line, " (backend=", "（后端=", 1)
		line = strings.Replace(line, ")\n", "）\n", 1)
	case strings.HasPrefix(line, "done: "):
		line = strings.Replace(line, "done: ", "完成：", 1)
		line = strings.Replace(line, " prompts in ", " 条提示词，输出到 ", 1)
	case strings.HasPrefix(line, "downloading "):
		line = strings.Replace(line, "downloading ", "正在下载 ", 1)
		line = strings.Replace(line, " prompts) -> ", " 条提示词）→ ", 1)
	case strings.HasPrefix(line, "retrying offset "):
		line = strings.Replace(line, "retrying offset ", "正在重试偏移量 ", 1)
		line = strings.Replace(line, " in ", "，等待 ", 1)
	}
	return w.writer.Write([]byte(line))
}

func localizedError(err error) string {
	if err == nil || detectCLILanguage() != cliChinese {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	message := err.Error()
	// These are the stable argument and policy diagnostics emitted by the Go
	// CLI. Wrapped filesystem/backend errors intentionally retain their English
	// technical detail so the original cause remains searchable.
	translations := []struct{ english, chinese string }{
		{"no such file or directory", "没有这样的文件或目录"},
		{"permission denied", "权限被拒绝"},
		{"is a directory", "是一个目录"},
		{"file exists", "文件已存在"},
		{"invalid argument", "参数无效"},
		{"requires a file or directory path", "需要文件或目录路径"},
		{"requires at least one file", "至少需要一个文件"},
		{"does not accept positional arguments", "不接受位置参数"},
		{"accepts at most one input path", "最多接受一个输入路径"},
		{"accepts only one of", "只能使用以下选项之一"},
		{"requires --output-dir", "需要 --output-dir"},
		{"requires --prompts and --out", "需要 --prompts 和 --out"},
		{"requires --replies and --out", "需要 --replies 和 --out"},
		{"requires --text or --file", "需要 --text 或 --file"},
		{"requires --s-star", "需要 --s-star"},
		{"must be at least 1", "必须至少为 1"},
		{"must be in (0,1]", "必须在 (0,1] 范围内"},
		{"must be in (0,1)", "必须在 (0,1) 范围内"},
		{"must be between 0 and 2", "必须在 0 到 2 之间"},
		{"must be non-negative", "必须为非负数"},
		{"must be positive", "必须为正数"},
		{"must be finite and non-negative", "必须是有限的非负数"},
		{"must be a non-empty comma-separated tactic@intensity list", "必须是非空的、以逗号分隔的 tactic@intensity 列表"},
		{"invalid --deep-images value", "无效的 --deep-images 值"},
		{"invalid --reflink value", "无效的 --reflink 值"},
		{"invalid --remove-pixel value", "无效的 --remove-pixel 值"},
		{"unknown --tactic", "未知的 --tactic"},
		{"unknown rewrite provider", "未知的重写 provider"},
		{"requires --in-place", "需要 --in-place"},
		{"--in-place requires a file path", "--in-place 需要文件路径"},
		{"--in-place requires a file input", "--in-place 需要文件输入"},
		{"--output and --output-dir cannot be used together", "--output 与 --output-dir 不能同时使用"},
		{"provide --sitemap URL or --base URL", "请提供 --sitemap URL 或 --base URL"},
		{"no sitemap found", "未找到 sitemap"},
		{"no URLs collected from sitemap", "sitemap 中未收集到 URL"},
		{"dataset exhausted before requested count", "数据集在达到请求数量前已耗尽"},
		{"must use http or https", "必须使用 http 或 https"},
		{"remote rewrite endpoint denied; pass --allow-remote explicitly", "远程重写端点被拒绝；请显式传入 --allow-remote"},
		{"refusing to detect binary-looking input as text", "拒绝将疑似二进制输入按文本检测"},
		{"in-place and --output cannot be used together", "--in-place 与 --output 不能同时使用"},
		{"--output is valid only for one input; use --output-dir for multiple files", "--output 仅适用于单个输入；多个文件请使用 --output-dir"},
		{"--in-place is not supported for directory input; choose --output-dir", "目录输入不支持 --in-place；请选择 --output-dir"},
		{"no watermark key; pass --key or set WATERMARKS_GUMBEL_KEY", "没有水印 key；请传入 --key 或设置 WATERMARKS_GUMBEL_KEY"},
		{"unknown command", "未知命令"},
		{"unknown stealer command", "未知 stealer 子命令"},
		{"invalid --format", "无效的 --format"},
	}
	for _, item := range translations {
		message = strings.ReplaceAll(message, item.english, item.chinese)
	}
	return message
}

func writeCLIError(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "aiwr: %s\n", localizedError(err))
	}
	return 2
}
