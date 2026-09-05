package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/iruanp/aiwr/internal/core"
)

const usageEnglish = `aiwr - inspect and remove AI watermark/provenance metadata

Usage:
  aiwr clean [options] FILE...
  aiwr inspect [options] FILE...
  aiwr detect [options] FILE...
  aiwr audit [options] DIRECTORY|FILE...
  aiwr rewrite-text [options] [FILE]
  aiwr stealer query|build|detect [options]
  aiwr download-prompts [options]
  aiwr serve [--host HOST] [--port PORT]

Direct format commands are also available:
  clean-file, inspect-file, clean-text, inspect-text,
  clean-image, inspect-image, clean-audio, inspect-audio,
  clean-video, inspect-video,
  score-stylometry, detect-gumbel, score-synthid,
  detect-text-watermark, markdiffusion, clean-ctrlregen,
  synthid-score-server, synthid-text-server,
  audit-website, check-staged, clean-staged, hook-written-file,
  bench-synthid-text, stealer, download-prompts

By default a file is written next to its source as NAME.cleaned.EXT. A
directory is written to a sibling directory named DIRECTORY.cleaned, with
relative names and subdirectories preserved. Use --in-place for a backup and
in-place replacement, or --output/--output-dir to choose a destination.
`

const usageChinese = `aiwr - 检查并移除 AI 水印/来源元数据

用法：
  aiwr clean [选项] 文件...
  aiwr inspect [选项] 文件...
  aiwr detect [选项] 文件...
  aiwr audit [选项] 目录|文件...
  aiwr rewrite-text [选项] [文件]
  aiwr stealer query|build|detect [选项]
  aiwr download-prompts [选项]
  aiwr serve [--host 主机] [--port 端口]

也可以使用直接格式命令：
  clean-file、inspect-file、clean-text、inspect-text、
  clean-image、inspect-image、clean-audio、inspect-audio、
  clean-video、inspect-video、score-stylometry、detect-gumbel、
  score-synthid、detect-text-watermark、markdiffusion、clean-ctrlregen、
  synthid-score-server、synthid-text-server、audit-website、check-staged、
  clean-staged、hook-written-file、bench-synthid-text、stealer、
  download-prompts

默认会将文件写到源文件旁的 NAME.cleaned.EXT。目录会写到相邻的
DIRECTORY.cleaned，并保留相对路径和子目录。使用 --in-place 原地替换
并创建备份，或使用 --output/--output-dir 指定目标。
`

type parsedCLI struct {
	opts        core.Options
	paths       []string
	sarif       bool
	stylometry  bool
	threshold   float64
	audit       bool
	auditFormat string
	auditSkip   string
	stats       bool
	explain     bool
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, localizedUsage())
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(localizedUsage())
		return 0
	case "version", "--version", "-v":
		fmt.Printf("%s\n", cliText(
			fmt.Sprintf("aiwr %s (Go reimplementation of watermarks-remover)", core.Version),
			fmt.Sprintf("aiwr %s（watermarks-remover 的 Go 重实现）", core.Version),
		))
		return 0
	case "clean", "clean-file", "clean_file":
		return runCleanMode(args[1:], "", "")
	case "clean-text", "clean_text":
		return runCleanMode(args[1:], "text", "")
	case "clean-image", "clean_image":
		return runCleanMode(args[1:], "image", "")
	case "clean-audio", "clean_audio":
		return runCleanMode(args[1:], "av", "audio")
	case "clean-video", "clean_video":
		return runCleanMode(args[1:], "av", "video")
	case "inspect", "inspect-file", "inspect_file":
		return runInspect(args[1:], "")
	case "inspect-text", "inspect_text":
		return runInspect(args[1:], "text")
	case "inspect-image", "inspect_image":
		return runInspect(args[1:], "image")
	case "inspect-audio", "inspect_audio", "inspect-video", "inspect_video":
		return runInspect(args[1:], "av")
	case "score-stylometry", "score_stylometry":
		return runStylometry(args[1:])
	case "detect-gumbel", "detect_gumbel":
		return runGumbel(args[1:])
	case "score-synthid", "score_synthid":
		return runExternalCommand(args[1:], "score_synthid.py")
	case "synthid-score-server", "synthid_score_server":
		return runExternalCommand(args[1:], "synthid_score_server.py")
	case "synthid-text-server", "synthid_text_server":
		return runExternalCommand(args[1:], "synthid_text_server.py")
	case "detect-text-watermark", "detect_text_watermark":
		return runExternalCommand(args[1:], "detect_text_watermark.py")
	case "markdiffusion", "markdiffusion-harness", "markdiffusion_harness":
		return runExternalCommand(args[1:], "markdiffusion_harness.py")
	case "clean-ctrlregen", "clean_ctrlregen":
		return runExternalCommand(args[1:], "clean_ctrlregen.py")
	case "audit-website", "audit_website":
		return runAuditWebsite(args[1:])
	case "check-staged", "check_staged":
		return runCheckStaged(args[1:])
	case "clean-staged", "clean_staged":
		return runCleanStaged(args[1:])
	case "hook-written-file", "hook_written_file":
		return runHookWrittenFile(args[1:])
	case "bench-synthid-text", "bench_synthid_text":
		return runExternalCommand(args[1:], "bench_synthid_text.py")
	case "detect":
		return runDetect(args[1:])
	case "audit", "audit-dir", "audit_dir":
		return runAudit(args[1:])
	case "rewrite-text", "rewrite_text":
		return runRewriteText(args[1:])
	case "stealer", "steal":
		return runStealer(args[1:])
	case "download-prompts", "download_prompts":
		return runDownloadPrompts(args[1:])
	case "serve", "server":
		return runServe(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "aiwr: %s %q\n\n%s", cliText("unknown command", "未知命令"), args[0], localizedUsage())
		return 2
	}
}

func parseCommon(command string, args []string, forced string) (parsedCLI, error) {
	opts := core.DefaultOptions()
	if forced != "" {
		opts.ForceType = forced
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	setLocalizedCommonUsage(fs, command)
	fs.StringVar(&opts.Output, "o", "", "output file (default: NAME.cleaned.EXT)")
	fs.StringVar(&opts.Output, "output", "", "output file")
	fs.StringVar(&opts.OutputDir, "output-dir", "", "output directory for directory or multi-file input")
	fs.BoolVar(&opts.InPlace, "in-place", false, "replace files in place and create FILE.bak")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON")
	fs.BoolVar(&opts.Quiet, "q", false, "suppress routine status output")
	fs.BoolVar(&opts.Quiet, "quiet", false, "suppress routine status output")
	fs.BoolVar(&opts.OnlyChanged, "only-changed", false, "write/report only changed files where applicable")
	fs.BoolVar(&opts.NFKC, "nfkc", false, "apply Unicode NFKC normalization")
	fs.BoolVar(&opts.AggressiveHomoglyphs, "aggressive-homoglyphs", false, "normalize selected Cyrillic/fullwidth homoglyphs")
	fs.BoolVar(&opts.AggressiveHomoglyphs, "aggressive", false, "alias for --aggressive-homoglyphs")
	noSpaces := false
	fs.BoolVar(&noSpaces, "no-normalize-spaces", false, "preserve non-breaking and space-like characters")
	fs.BoolVar(&opts.StripEmojiGlue, "strip-emoji-glue", false, "remove ZWJ/variation selectors when explicitly requested")
	fs.BoolVar(&opts.StripBidi, "strip-bidi", false, "remove bidi controls, including valid embeddings")
	fs.BoolVar(&opts.KeepNonAIMetadata, "keep-non-ai-metadata", false, "remove only strongly AI/provenance-like metadata")
	noLayerAText := false
	fs.BoolVar(&noLayerAText, "no-layer-a-text", false, "do not scrub visible text bodies inside HTML/office containers")
	fs.StringVar(&opts.ForceType, "as", opts.ForceType, "force type: auto, text, image, container, av")
	fs.BoolVar(&opts.ForceText, "force-text", false, "allow binary-looking input through the text pipeline")
	fs.StringVar((*string)(&opts.DeepImages), "deep-images", string(opts.DeepImages), "embedded image policy: auto, always, lossless, never")
	fs.StringVar((*string)(&opts.Reflink), "reflink", string(opts.Reflink), "unchanged-file copy policy: auto, always, never")
	fs.IntVar(&opts.Jobs, "jobs", opts.Jobs, "parallelism hint for batch operations")
	fs.IntVar(&opts.Jobs, "j", opts.Jobs, "alias for --jobs")
	fs.BoolVar(&opts.SkipUnknown, "skip-unknown", false, "skip unknown files in directory batches")
	fs.StringVar(&opts.UpstreamScriptsDir, "upstream-scripts", "", "upstream service/scripts directory for optional Python/GPU adapters")
	fs.StringVar(&opts.UpstreamScriptsDir, "upstream-dir-scripts", "", "alias for --upstream-scripts")
	fs.StringVar(&opts.SynthIDDir, "synthid-dir", "", "reverse-SynthID checkout root for optional pixel scoring")
	fs.StringVar(&opts.RemovePixel, "remove-pixel", "", "pixel remover: ctrlregen or diffusion")
	fs.StringVar(&opts.CtrlRegenDir, "ctrlregen-dir", "", "CtrlRegen/noai-watermark checkout root")
	fs.Float64Var(&opts.CtrlRegenIntensity, "ctrlregen-intensity", opts.CtrlRegenIntensity, "CtrlRegen regeneration intensity in (0,1]")
	fs.IntVar(&opts.CtrlRegenSteps, "ctrlregen-steps", opts.CtrlRegenSteps, "CtrlRegen diffusion steps")
	fs.StringVar(&opts.CtrlRegenDevice, "ctrlregen-device", "", "CtrlRegen device: auto|cpu|cuda|mps")
	ctrlSeed := opts.CtrlRegenSeed
	fs.IntVar(&ctrlSeed, "ctrlregen-seed", ctrlSeed, "optional CtrlRegen RNG seed")
	fs.IntVar(&opts.CtrlRegenTimeout, "ctrlregen-timeout", opts.CtrlRegenTimeout, "CtrlRegen timeout in seconds")
	fs.StringVar(&opts.MarkDiffusionDir, "markdiffusion-dir", "", "MarkDiffusion checkout root")
	fs.Float64Var(&opts.MarkDiffusionIntensity, "markdiffusion-intensity", opts.MarkDiffusionIntensity, "DiffusionPurification intensity in (0,1]")
	fs.StringVar(&opts.MarkDiffusionModel, "markdiffusion-model", "", "Stable Diffusion model for purification")
	fs.IntVar(&opts.MarkDiffusionSize, "markdiffusion-size", opts.MarkDiffusionSize, "DiffusionPurification working size")
	fs.IntVar(&opts.MarkDiffusionSteps, "markdiffusion-steps", opts.MarkDiffusionSteps, "DiffusionPurification steps")
	fs.StringVar(&opts.MarkDiffusionDevice, "markdiffusion-device", "", "DiffusionPurification device: auto|cpu|cuda|mps")
	fs.IntVar(&opts.MarkDiffusionTimeout, "markdiffusion-timeout", opts.MarkDiffusionTimeout, "MarkDiffusion timeout in seconds")
	fs.Float64Var(&opts.VideoVoteThreshold, "vote-threshold", opts.VideoVoteThreshold, "video temporal vote threshold")
	frameFraction := opts.VideoFrameFraction
	fs.Float64Var(&frameFraction, "frame-fraction", frameFraction, "fraction of video frames to purify")
	fs.BoolVar(&opts.AudioRemix, "remix-audio", false, "apply optional ffmpeg audio remix")
	fs.BoolVar(&opts.AudioRemix, "audio-remix", false, "alias for --remix-audio")
	fs.Float64Var(&opts.AudioTempo, "audio-tempo", opts.AudioTempo, "ffmpeg tempo factor, 0.5..2.0")
	fs.Float64Var(&opts.AudioTempo, "tempo", opts.AudioTempo, "alias for --audio-tempo")
	fs.Float64Var(&opts.AudioPitch, "audio-pitch", opts.AudioPitch, "ffmpeg pitch shift in semitones")
	fs.Float64Var(&opts.AudioPitch, "pitch-semitones", opts.AudioPitch, "alias for --audio-pitch")
	fs.StringVar(&opts.AudioBitrate, "audio-bitrate", opts.AudioBitrate, "audio bitrate for destructive remix")
	fs.StringVar(&opts.AudioBitrate, "reencode-bitrate", opts.AudioBitrate, "alias for --audio-bitrate")
	fs.StringVar(&opts.AudioCodec, "codec", "", "audio output codec override")
	fs.IntVar(&opts.FFmpegTimeoutSeconds, "ffmpeg-timeout", opts.FFmpegTimeoutSeconds, "ffmpeg timeout in seconds")
	// These inspection switches are accepted for parity with the upstream
	// scripts. The deterministic implementation always includes Layer A data;
	// --stylometry adds a compact local statistical summary below.
	stats := false
	stylometry := false
	fs.BoolVar(&stats, "stats", false, "include text cleaning statistics")
	fs.BoolVar(&stylometry, "stylometry", false, "include text stylometry")
	threshold := opts.Threshold
	fs.Float64Var(&threshold, "threshold", threshold, "statistical suspicion threshold")
	var audit bool
	fs.BoolVar(&audit, "audit", false, "audit rather than clean where supported")
	sarif := false
	fs.BoolVar(&sarif, "sarif", false, "emit SARIF audit output")
	auditFormat := "human"
	fs.StringVar(&auditFormat, "format", auditFormat, "audit output: human, json, or sarif")
	auditSkip := ""
	fs.StringVar(&auditSkip, "skip", auditSkip, "comma-separated directory names to skip during audit")
	checkStylometry := false
	fs.BoolVar(&checkStylometry, "check-stylometry", false, "include stylometry in audit output")
	explain := false
	fs.BoolVar(&explain, "explain", false, "include detailed stylometry marker matches")
	if err := fs.Parse(normalizeFlagArgs(args)); err != nil {
		return parsedCLI{}, err
	}
	if noSpaces {
		opts.NormalizeSpaces = false
	}
	opts.AlsoLayerAText = !noLayerAText
	opts.Stylometry = stylometry || checkStylometry || command == "inspect" && stats
	opts.Threshold = threshold
	if opts.Threshold <= 0 || opts.Threshold > 1 {
		return parsedCLI{}, errors.New("--threshold must be in (0,1]")
	}
	if opts.Jobs < 1 {
		return parsedCLI{}, errors.New("--jobs must be at least 1")
	}
	if opts.DeepImages != core.DeepAuto && opts.DeepImages != core.DeepAlways && opts.DeepImages != core.DeepLossless && opts.DeepImages != core.DeepNever {
		return parsedCLI{}, fmt.Errorf("invalid --deep-images value %q", opts.DeepImages)
	}
	if opts.Reflink != core.ReflinkAuto && opts.Reflink != core.ReflinkAlways && opts.Reflink != core.ReflinkNever {
		return parsedCLI{}, fmt.Errorf("invalid --reflink value %q", opts.Reflink)
	}
	if opts.ForceType == "audio" || opts.ForceType == "video" {
		opts.ForceType = "av"
	}
	opts.CtrlRegenSeed = ctrlSeed
	opts.CtrlRegenSeedSet = false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ctrlregen-seed" {
			opts.CtrlRegenSeedSet = true
		}
	})
	if strings.Contains(strings.Join(args, "\x00"), "--frame-fraction") {
		opts.VideoFrameFraction = frameFraction
		opts.VideoFrameFractionSet = true
	}
	if opts.RemovePixel != "" && opts.RemovePixel != "ctrlregen" && opts.RemovePixel != "diffusion" {
		return parsedCLI{}, fmt.Errorf("invalid --remove-pixel value %q", opts.RemovePixel)
	}
	if opts.VideoVoteThreshold <= 0 || opts.VideoVoteThreshold > 1 {
		return parsedCLI{}, errors.New("--vote-threshold must be in (0,1]")
	}
	return parsedCLI{opts: opts, paths: fs.Args(), sarif: sarif, stylometry: stylometry || command == "inspect" && stats, threshold: threshold, audit: audit, auditFormat: auditFormat, auditSkip: auditSkip, stats: stats, explain: explain}, nil
}

func runClean(args []string, forced string) int {
	return runCleanMode(args, forced, "")
}

func mediaOutputName(input, suffix string) string {
	ext := filepath.Ext(input)
	return strings.TrimSuffix(filepath.Base(input), ext) + suffix + ext
}

func audioOutputName(input string) string {
	ext := filepath.Ext(input)
	return strings.TrimSuffix(filepath.Base(input), ext) + ".audio.m4a"
}

func runExternalCommand(args []string, scriptName string) int {
	forwarded := make([]string, 0, len(args))
	scriptsDir := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--upstream-scripts" || arg == "--upstream-dir-scripts":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return cliError(fmt.Errorf("%s requires a directory", arg))
			}
			scriptsDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--upstream-scripts="):
			scriptsDir = strings.TrimPrefix(arg, "--upstream-scripts=")
		case strings.HasPrefix(arg, "--upstream-dir-scripts="):
			scriptsDir = strings.TrimPrefix(arg, "--upstream-dir-scripts=")
		default:
			if target, ok := externalDirectoryAlias(scriptName, arg); ok {
				if strings.Contains(arg, "=") {
					value := arg[strings.IndexByte(arg, '=')+1:]
					if strings.TrimSpace(value) == "" {
						return cliError(fmt.Errorf("%s requires a directory", strings.SplitN(arg, "=", 2)[0]))
					}
					forwarded = append(forwarded, target, value)
					continue
				}
				if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
					return cliError(fmt.Errorf("%s requires a directory", arg))
				}
				forwarded = append(forwarded, target, args[i+1])
				i++
				continue
			}
			forwarded = append(forwarded, arg)
		}
	}
	if externalHelpRequested(args) && strings.TrimSpace(scriptsDir) == "" && !externalScriptsConfigured() && !bundledExternalScriptAvailable(scriptName) {
		printExternalHelp(scriptName)
		return 0
	}
	opts := core.DefaultOptions()
	opts.UpstreamScriptsDir = scriptsDir
	code, err := core.RunExternalCLI(scriptName, forwarded, opts)
	if err != nil {
		return cliError(err)
	}
	return code
}

func externalHelpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func externalScriptsConfigured() bool {
	for _, name := range []string{"AIWR_UPSTREAM_SCRIPTS", "WATERMARKS_UPSTREAM_SCRIPTS", "WATERMARKS_UPSTREAM_REPO"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func bundledExternalScriptAvailable(scriptName string) bool {
	candidates := []string{filepath.Join("service", "scripts", scriptName)}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(dir, "service", "scripts", scriptName),
			filepath.Join(dir, "..", "service", "scripts", scriptName),
		)
	}
	if working, err := os.Getwd(); err == nil {
		current := working
		for i := 0; i < 4; i++ {
			candidates = append(candidates, filepath.Join(current, "service", "scripts", scriptName))
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func printExternalHelp(scriptName string) {
	command := strings.TrimSuffix(scriptName, ".py")
	name := strings.ReplaceAll(command, "_", "-")
	fmt.Fprintf(os.Stdout, "aiwr %s\n\n", name)
	fmt.Fprintln(os.Stdout, cliText(
		fmt.Sprintf("This command delegates to upstream %s.", scriptName),
		fmt.Sprintf("此命令委托给上游 %s。", scriptName),
	))
	fmt.Fprintln(os.Stdout, cliText(
		"Provide --upstream-scripts PATH (or set AIWR_UPSTREAM_SCRIPTS) to see and run the upstream command options.",
		"请提供 --upstream-scripts PATH（或设置 AIWR_UPSTREAM_SCRIPTS）以查看并运行上游命令选项。",
	))
	fmt.Fprintf(os.Stdout, "%s\n", cliText(
		fmt.Sprintf("Usage: aiwr %s [--upstream-scripts PATH] [upstream options]", name),
		fmt.Sprintf("用法：aiwr %s [--upstream-scripts PATH] [上游选项]", name),
	))
}

// externalDirectoryAlias keeps aiwr's option names consistent across the
// optional adapters while preserving the upstream scripts' own CLI. The
// adapter checkout (service/scripts) and the heavy backend checkout are
// intentionally separate directories.
func externalDirectoryAlias(scriptName, arg string) (string, bool) {
	name := arg
	if at := strings.IndexByte(name, '='); at >= 0 {
		name = name[:at]
	}
	switch {
	case scriptName == "score_synthid.py" && (name == "--synthid-dir" || name == "--reverse-synthid-dir"):
		return "--upstream-dir", true
	case scriptName == "clean_ctrlregen.py" && name == "--ctrlregen-dir":
		return "--upstream-dir", true
	case scriptName == "markdiffusion_harness.py" && name == "--markdiffusion-dir":
		return "--upstream-dir", true
	case scriptName == "detect_text_watermark.py" && name == "--markllm-dir":
		return "--upstream-dir", true
	default:
		return "", false
	}
}

func runCleanMode(args []string, forced, mediaMode string) int {
	parsed, err := parseCommon("clean", args, forced)
	if err != nil {
		return cliError(err)
	}
	if len(parsed.paths) == 0 {
		if forced != "text" {
			return cliError(errors.New("clean requires a file or directory path"))
		}
		return cleanStdin(parsed.opts, parsed.stats)
	}
	if mediaMode == "audio" {
		parsed.opts.AudioRemix = true
	} else if mediaMode == "video" {
		if parsed.opts.RemovePixel == "" {
			return cliError(errors.New("clean-video requires --remove-pixel ctrlregen|diffusion"))
		}
		parsed.opts.AudioRemix = false
	}
	if len(parsed.paths) == 1 && parsed.paths[0] == "-" {
		return cleanStdin(parsed.opts, parsed.stats)
	}
	if parsed.opts.InPlace && parsed.opts.Output != "" {
		return cliError(errors.New("--in-place and --output cannot be used together"))
	}
	if len(parsed.paths) > 1 && parsed.opts.Output != "" {
		return cliError(errors.New("--output is valid only for one input; use --output-dir for multiple files"))
	}
	if len(parsed.paths) > 1 && parsed.opts.OutputDir == "" {
		return cliError(errors.New("multiple inputs require --output-dir"))
	}

	var results []core.CleanResult
	var summaries []core.BatchSummary
	errorsCount := 0
	residualFound := false
	for _, path := range parsed.paths {
		st, statErr := os.Stat(path)
		if statErr != nil {
			errorsCount++
			if !parsed.opts.JSON {
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(statErr))
			}
			continue
		}
		if st.IsDir() {
			if len(parsed.paths) != 1 {
				errorsCount++
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", cliText("directory input must be processed alone", "目录输入必须单独处理"), path)
				continue
			}
			local := parsed.opts
			if local.OutputDir == "" && local.Output != "" {
				local.OutputDir = local.Output
				local.Output = ""
			}
			if local.InPlace {
				errorsCount++
				fmt.Fprintf(os.Stderr, "aiwr: %s\n", cliText("--in-place is not supported for directory input; choose --output-dir", "目录输入不支持 --in-place；请选择 --output-dir"))
				continue
			}
			summary, cleanErr := core.CleanDirectory(path, local)
			if cleanErr != nil {
				errorsCount++
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(cleanErr))
				continue
			}
			summaries = append(summaries, summary)
			errorsCount += summary.Errors
			for _, item := range summary.Items {
				if item.Result != nil && cleanResultHasResidual(*item.Result) {
					residualFound = true
				}
			}
			if !parsed.opts.JSON && !parsed.opts.Quiet {
				fmt.Fprintf(os.Stderr, "%s\n", cliText(
					fmt.Sprintf("%s -> %s: %d files, %d changed, %d errors", summary.Input, summary.Output, summary.Files, summary.Changed, summary.Errors),
					fmt.Sprintf("%s → %s：%d 个文件，%d 个已变化，%d 个错误", summary.Input, summary.Output, summary.Files, summary.Changed, summary.Errors),
				))
			}
			continue
		}
		local := parsed.opts
		if local.OutputDir != "" {
			base := filepath.Base(path)
			if mediaMode == "audio" && parsed.opts.Output == "" {
				base = audioOutputName(path)
			} else if mediaMode == "video" && parsed.opts.Output == "" {
				base = mediaOutputName(path, ".video")
			}
			local.Output = filepath.Join(local.OutputDir, base)
		} else if mediaMode == "audio" && local.Output == "" && !local.InPlace {
			local.Output = filepath.Join(filepath.Dir(path), audioOutputName(path))
		} else if mediaMode == "video" && local.Output == "" && !local.InPlace {
			local.Output = filepath.Join(filepath.Dir(path), mediaOutputName(path, ".video"))
		}
		result, cleanErr := core.CleanFile(path, local)
		if cleanErr != nil {
			errorsCount++
			if !parsed.opts.JSON {
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(cleanErr))
			}
			continue
		}
		results = append(results, result)
		if parsed.stats && result.Kind == core.KindText && result.Stats != nil {
			writeJSONTo(os.Stderr, result.Stats)
		}
		if cleanResultHasResidual(result) {
			residualFound = true
		}
		if !parsed.opts.JSON && (!parsed.opts.Quiet || cleanResultNeedsReport(result)) && (!parsed.opts.OnlyChanged || cleanResultNeedsReport(result)) {
			printCleanHuman(result)
		}
	}
	emittedResults := results
	if parsed.opts.Quiet || parsed.opts.OnlyChanged {
		emittedResults = emittedResults[:0]
		for _, result := range results {
			if cleanResultNeedsReport(result) {
				emittedResults = append(emittedResults, result)
			}
		}
	}
	if parsed.opts.JSON {
		if len(summaries) > 0 {
			writeJSON(map[string]any{"summaries": summaries, "results": emittedResults, "errors": errorsCount})
		} else if len(emittedResults) == 1 {
			writeJSON(cleanCLIJSONReport(emittedResults[0]))
		} else if len(emittedResults) > 0 || errorsCount > 0 {
			values := make([]any, 0, len(emittedResults))
			for _, result := range emittedResults {
				values = append(values, cleanCLIJSONReport(result))
			}
			writeJSON(map[string]any{"results": values, "errors": errorsCount})
		} else {
			// --quiet/--only-changed intentionally produce no report for a
			// successful unchanged run, matching the upstream CLI.
		}
	}
	if errorsCount > 0 {
		if len(parsed.paths) == 1 && len(summaries) == 0 {
			return 2
		}
		return 3
	}
	if residualFound {
		return 1
	}
	return 0
}

func cleanStdin(opts core.Options, showStats bool) int {
	if opts.InPlace {
		return cliError(errors.New("--in-place requires a file path"))
	}
	if opts.Output != "" && opts.OutputDir != "" {
		return cliError(errors.New("--output and --output-dir cannot be used together"))
	}
	data, err := readStdin()
	if err != nil {
		return cliError(err)
	}
	result, cleaned, err := core.CleanBytes(data, "stdin.txt", opts)
	if err != nil {
		return cliError(err)
	}
	if showStats && result.Stats != nil {
		writeJSONTo(os.Stderr, result.Stats)
	}
	if opts.Output != "" {
		if err := core.WriteFileAtomic(opts.Output, cleaned, 0o644); err != nil {
			return cliError(err)
		}
		result.Output = opts.Output
	} else if opts.JSON {
		result.Output = "stdout"
		writeJSON(map[string]any{"result": result, "cleaned": string(cleaned)})
		if cleanResultHasResidual(result) {
			return 1
		}
		return 0
	} else {
		_, _ = os.Stdout.Write(cleaned)
	}
	if opts.JSON {
		writeJSON(cleanCLIJSONReport(result))
	}
	if cleanResultHasResidual(result) {
		return 1
	}
	return 0
}

// cleanCLIJSONReport is the single-file JSON contract of the upstream
// clean_file.py command. The Go CleanResult keeps extra operational fields for
// the HTTP API and directory batches; the CLI's one-file report stays
// compatible with the upstream scripts.
func cleanCLIJSONReport(result core.CleanResult) map[string]any {
	if result.Kind == core.KindText {
		return map[string]any{
			"kind":    string(result.Kind),
			"input":   result.Input,
			"output":  result.Output,
			"stats":   result.Stats,
			"changed": result.Changed,
		}
	}
	report := map[string]any{
		"kind":                  string(result.Kind),
		"input":                 result.Input,
		"output":                result.Output,
		"format":                result.Format,
		"actions":               nonNilCLIStrings(result.Actions),
		"bytes_in":              result.BytesIn,
		"bytes_out":             result.BytesOut,
		"changed":               result.Changed,
		"still_has_c2pa":        result.StillHasC2PA,
		"still_has_ai_metadata": result.StillHasAI,
		"post_findings":         nonNilCLIStrings(result.PostFindings),
	}
	if result.Kind == core.KindImage {
		report["synthid_before"] = result.SynthIDBefore
		report["synthid_after"] = result.SynthIDAfter
		report["pixel_removal"] = result.PixelRemoval
	} else if result.Kind == core.KindContainer {
		report["meta"] = map[string]any{"format": result.Format}
	}
	return report
}

func nonNilCLIStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func cleanResultHasResidual(result core.CleanResult) bool {
	if result.StillHasC2PA || result.StillHasAI {
		return true
	}
	if result.PixelRemoval != nil {
		if available, ok := result.PixelRemoval["available"].(bool); ok && !available {
			return true
		}
	}
	return false
}

func cleanResultNeedsReport(result core.CleanResult) bool {
	return result.Changed || cleanResultHasResidual(result) || result.Partial || len(result.Warnings) > 0
}

func runInspect(args []string, forced string) int {
	parsed, err := parseCommon("inspect", args, forced)
	if err != nil {
		return cliError(err)
	}
	if parsed.audit {
		if forced != "text" {
			return cliError(errors.New("--audit is only supported by inspect-text"))
		}
		if len(parsed.paths) > 1 {
			return cliError(errors.New("inspect-text --audit accepts at most one input"))
		}
		if len(parsed.paths) == 1 && parsed.paths[0] != "-" {
			st, statErr := os.Stat(parsed.paths[0])
			if statErr != nil {
				return cliError(statErr)
			}
			if st.IsDir() {
				return cliError(errors.New("inspect-text --audit accepts a file or stdin, not a directory"))
			}
		}
		parsed.opts.Stylometry = true
	}
	if len(parsed.paths) == 0 {
		if forced != "text" {
			return cliError(errors.New("inspect requires a file or directory path"))
		}
		data, readErr := readStdin()
		if readErr != nil {
			return cliError(readErr)
		}
		report, inspectErr := core.InspectBytes(data, "stdin.txt", parsed.opts)
		if inspectErr != nil {
			return cliError(inspectErr)
		}
		if parsed.audit {
			auditText := makeTextAuditReport(report, report.Path)
			writeJSON(auditText)
			if textAuditSuspicious(auditText, report) {
				return 1
			}
			return 0
		}
		if parsed.opts.JSON {
			writeJSON(inspectJSONReport(report))
		} else {
			printReportHuman(report, parsed.stylometry, parsed.threshold)
		}
		if suspiciousReport(report) {
			return 1
		}
		return 0
	}
	if len(parsed.paths) == 1 && parsed.paths[0] == "-" {
		data, readErr := readStdin()
		if readErr != nil {
			return cliError(readErr)
		}
		report, inspectErr := core.InspectBytes(data, "stdin.txt", parsed.opts)
		if inspectErr != nil {
			return cliError(inspectErr)
		}
		if parsed.audit {
			auditText := makeTextAuditReport(report, report.Path)
			writeJSON(auditText)
			if textAuditSuspicious(auditText, report) {
				return 1
			}
			return 0
		}
		if parsed.opts.JSON {
			writeJSON(inspectJSONReport(report))
		} else {
			printReportHuman(report, parsed.stylometry, parsed.threshold)
		}
		if suspiciousReport(report) {
			return 1
		}
		return 0
	}

	reports := []core.FileReport{}
	errorsCount := 0
	for _, path := range parsed.paths {
		st, statErr := os.Stat(path)
		if statErr != nil {
			errorsCount++
			if !parsed.opts.JSON {
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(statErr))
			}
			continue
		}
		if st.IsDir() {
			batch, inspectErr := core.InspectDirectory(path, parsed.opts)
			if inspectErr != nil {
				errorsCount++
				if !parsed.opts.JSON {
					fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(inspectErr))
				}
			}
			reports = append(reports, batch...)
			continue
		}
		report, inspectErr := core.InspectFile(path, parsed.opts)
		if inspectErr != nil {
			errorsCount++
			if !parsed.opts.JSON {
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(inspectErr))
			}
			continue
		}
		if parsed.audit {
			auditText := makeTextAuditReport(report, report.Path)
			writeJSON(auditText)
			if textAuditSuspicious(auditText, report) {
				return 1
			}
			return 0
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Path < reports[j].Path })
	if parsed.opts.JSON {
		if len(reports) == 1 && errorsCount == 0 {
			writeJSON(inspectJSONReport(reports[0]))
		} else {
			values := make([]any, 0, len(reports))
			for _, report := range reports {
				values = append(values, inspectJSONReport(report))
			}
			writeJSON(map[string]any{"reports": values, "errors": errorsCount})
		}
	} else {
		for _, report := range reports {
			printReportHuman(report, parsed.stylometry, parsed.threshold)
		}
	}
	if errorsCount > 0 {
		return 3
	}
	for _, report := range reports {
		if suspiciousReport(report) {
			return 1
		}
	}
	return 0
}

func inspectJSONReport(report core.FileReport) core.FileReport {
	if report.Kind == core.KindUnknown && report.Path != "" {
		if absolute, err := filepath.Abs(report.Path); err == nil {
			report.Path = absolute
		}
	}
	if report.Kind == core.KindText && report.Path != "" && report.Path != "stdin.txt" {
		if absolute, err := filepath.Abs(report.Path); err == nil {
			report.Path = absolute
		}
	}
	return report
}

type detection struct {
	Path       string    `json:"path"`
	Kind       core.Kind `json:"kind"`
	Format     string    `json:"format,omitempty"`
	Suspicious bool      `json:"suspicious"`
	Findings   []string  `json:"findings,omitempty"`
	Detections []any     `json:"detections,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func runDetect(args []string) int {
	parsed, err := parseCommon("detect", args, "")
	if err != nil {
		return cliError(err)
	}
	if len(parsed.paths) == 0 {
		return cliError(errors.New("detect requires a file or directory path"))
	}
	items := []detection{}
	errorsCount := 0
	for _, path := range parsed.paths {
		st, statErr := os.Stat(path)
		if statErr != nil {
			errorsCount++
			items = append(items, detection{Path: path, Error: statErr.Error()})
			continue
		}
		if st.IsDir() {
			reports, inspectErr := core.InspectDirectory(path, parsed.opts)
			if inspectErr != nil {
				errorsCount++
			}
			for _, report := range reports {
				detected, detections, detectionsErr := core.DetectFile(report.Path, parsed.opts)
				if detectionsErr != nil {
					errorsCount++
					items = append(items, detection{Path: report.Path, Error: detectionsErr.Error()})
					continue
				}
				items = append(items, detectionFromReport(detected, detections))
			}
			continue
		}
		report, detections, detectErr := core.DetectFile(path, parsed.opts)
		if detectErr != nil {
			errorsCount++
			items = append(items, detection{Path: path, Error: detectErr.Error()})
			continue
		}
		items = append(items, detectionFromReport(report, detections))
	}
	if parsed.opts.JSON {
		writeJSON(map[string]any{"detections": items, "errors": errorsCount})
	} else {
		for _, item := range items {
			if item.Error != "" {
				fmt.Fprintf(os.Stderr, "%s: %s\n", item.Path, localizedError(errors.New(item.Error)))
			} else {
				fmt.Printf("%s\n", cliText(
					fmt.Sprintf("%s: %s/%s%s", item.Path, item.Kind, item.Format, suspiciousSuffix(item.Suspicious)),
					fmt.Sprintf("%s：%s/%s%s", item.Path, item.Kind, item.Format, suspiciousSuffix(item.Suspicious)),
				))
				for _, raw := range item.Detections {
					if detector, ok := raw.(map[string]any); ok {
						fmt.Printf("  %s %v：%v\n", cliText("detector", "检测器"), detector["detector"], detector)
					}
				}
			}
		}
	}
	if errorsCount > 0 {
		return 3
	}
	for _, item := range items {
		if item.Suspicious {
			return 1
		}
	}
	return 0
}

func detectionFromReport(report core.FileReport, detections []any) detection {
	return detection{Path: report.Path, Kind: report.Kind, Format: report.Format, Suspicious: suspiciousReport(report) || core.DetectionsSuspicious(detections), Findings: report.Findings, Detections: detections}
}

func runAudit(args []string) int {
	parsed, err := parseCommon("audit", args, "")
	if err != nil {
		return cliError(err)
	}
	if len(parsed.paths) == 0 {
		return cliError(errors.New("audit requires a file or directory path"))
	}
	format := strings.ToLower(strings.TrimSpace(parsed.auditFormat))
	if parsed.sarif {
		format = "sarif"
	} else if parsed.opts.JSON {
		format = "json"
	}
	if format != "human" && format != "json" && format != "sarif" {
		return cliError(fmt.Errorf("invalid --format %q; choose human, json, or sarif", parsed.auditFormat))
	}
	items := []map[string]any{}
	skipped := []map[string]any{}
	root := ""
	for _, input := range parsed.paths {
		st, statErr := os.Stat(input)
		if statErr != nil {
			skipped = append(skipped, map[string]any{"path": input, "reason": statErr.Error()})
			continue
		}
		if st.IsDir() {
			if root == "" {
				root, _ = filepath.Abs(input)
			}
			files, missed := auditDirectory(input, parsed.opts, parsed.auditSkip)
			items = append(items, files...)
			skipped = append(skipped, missed...)
			continue
		}
		item, miss := auditOne(input, parsed.opts)
		if miss != nil {
			skipped = append(skipped, miss)
		} else {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["path"]) < fmt.Sprint(items[j]["path"]) })
	sort.Slice(skipped, func(i, j int) bool { return fmt.Sprint(skipped[i]["path"]) < fmt.Sprint(skipped[j]["path"]) })
	if len(parsed.paths) != 1 {
		root = ""
	}
	summary := aggregateAudit(items)
	report := map[string]any{"root": root, "files_scanned": len(items), "files_skipped": skipped, "summary": summary, "files": items}
	if format == "sarif" {
		writeJSON(makeAuditSARIF(report))
	} else if format == "json" {
		writeJSON(report)
	} else {
		printAuditHuman(report)
	}
	if len(skipped) > 0 {
		return 3
	}
	if actionable, ok := summary["actionable_files"].(int); ok && actionable > 0 {
		return 1
	}
	return 0
}

var defaultAuditSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	"dist": true, "build": true, ".next": true, "target": true, ".cache": true,
}

func auditDirectory(root string, opts core.Options, extra string) ([]map[string]any, []map[string]any) {
	root, _ = filepath.Abs(root)
	skipDirs := map[string]bool{}
	for name := range defaultAuditSkipDirs {
		skipDirs[name] = true
	}
	for _, name := range strings.Split(extra, ",") {
		if name = strings.TrimSpace(name); name != "" {
			skipDirs[name] = true
		}
	}
	paths := []string{}
	var walkErrs []map[string]any
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			walkErrs = append(walkErrs, map[string]any{"path": current, "reason": walkErr.Error()})
			return nil
		}
		if entry.IsDir() {
			if current != root && (skipDirs[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			walkErrs = append(walkErrs, map[string]any{"path": current, "reason": infoErr.Error()})
			return nil
		}
		if info.Mode().IsRegular() {
			paths = append(paths, current)
		}
		return nil
	})
	if err != nil {
		walkErrs = append(walkErrs, map[string]any{"path": root, "reason": err.Error()})
	}
	items := make([]map[string]any, 0, len(paths))
	skipped := append([]map[string]any{}, walkErrs...)
	if opts.Jobs <= 1 {
		for _, path := range paths {
			item, miss := auditOne(path, opts)
			if miss != nil {
				skipped = append(skipped, miss)
			} else {
				items = append(items, item)
			}
		}
		return items, skipped
	}
	type auditResult struct {
		item map[string]any
		miss map[string]any
	}
	jobs := make(chan string)
	results := make(chan auditResult, len(paths))
	workers := opts.Jobs
	if workers > len(paths) {
		workers = len(paths)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				item, miss := auditOne(path, opts)
				results <- auditResult{item: item, miss: miss}
			}
		}()
	}
	go func() {
		for _, path := range paths {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for result := range results {
		if result.miss != nil {
			skipped = append(skipped, result.miss)
		} else {
			items = append(items, result.item)
		}
	}
	return items, skipped
}

func auditOne(path string, opts core.Options) (map[string]any, map[string]any) {
	// The upstream audit reads known text extensions as UTF-8 with
	// surrogateescape and deliberately does not apply the text-only binary
	// guard.  Keep that behavior for audit scans (not for inspect/clean): this
	// matters for legitimate UTF-16 localization files, whose NUL bytes would
	// otherwise be reported as a skipped file rather than a clean text file.
	auditOpts := opts
	if kind, classifyErr := core.Classify(path); classifyErr == nil && kind == core.KindText {
		auditOpts.ForceText = true
	}
	report, err := core.InspectFile(path, auditOpts)
	if err != nil {
		return nil, map[string]any{"path": path, "reason": err.Error()}
	}
	return auditItem(report, opts), nil
}

func auditItem(report core.FileReport, opts core.Options) map[string]any {
	kind := string(report.Kind)
	if report.Format != "" {
		kind = report.Format
	}
	findings := append([]string(nil), report.Findings...)
	confidence := append([]string(nil), report.FindingsConfidence...)
	suspicious := report.SuspiciousTotal
	if report.Text != nil {
		for _, hit := range report.Text.Hits {
			findings = append(findings, fmt.Sprintf("layer-a [%s] %s x%d", hit.Kind, hit.Label, hit.Count))
			if hit.Kind == "space" {
				confidence = append(confidence, "informational")
			} else {
				confidence = append(confidence, "probable")
			}
		}
		if opts.Stylometry && core.StylometrySuspiciousAt(report.Text.Stylometry, opts.Threshold) {
			if score, ok := report.Text.Stylometry["score"].(float64); ok {
				findings = append(findings, fmt.Sprintf("stylometry [high_probability] score %.2f", score))
				confidence = append(confidence, "probable")
				suspicious++
			}
		}
	}
	item := map[string]any{
		"path": pathForAudit(report.Path), "kind": kind, "has_c2pa": report.HasC2PA,
		"has_ai_metadata": report.HasAIMetadata, "suspicious_total": suspicious,
		"findings": findings, "confidence": confidence, "notes": report.Notes,
	}
	if report.Text != nil && report.Text.Stylometry != nil && opts.Stylometry {
		item["stylometry"] = report.Text.Stylometry
	}
	return item
}

func pathForAudit(path string) string { return path }

func aggregateAudit(items []map[string]any) map[string]any {
	byKind := map[string]int{}
	confidenceCounts := map[string]int{"confirmed": 0, "probable": 0, "informational": 0, "likely_false_positive": 0}
	withC2PA, withAI, suspiciousText, actionable := 0, 0, 0, 0
	for _, item := range items {
		kind := fmt.Sprint(item["kind"])
		byKind[kind]++
		if value, _ := item["has_c2pa"].(bool); value {
			withC2PA++
		}
		if value, _ := item["has_ai_metadata"].(bool); value {
			withAI++
		}
		if value, _ := item["suspicious_total"].(int); value > 0 {
			suspiciousText++
		}
		itemActionable := false
		if value, _ := item["has_c2pa"].(bool); value {
			itemActionable = true
		}
		if values, ok := item["confidence"].([]string); ok {
			for _, value := range values {
				if _, exists := confidenceCounts[value]; exists {
					confidenceCounts[value]++
				}
				if value == "confirmed" || value == "probable" {
					itemActionable = true
				}
			}
		}
		if itemActionable {
			actionable++
		}
	}
	return map[string]any{"total": len(items), "by_kind": byKind, "with_c2pa": withC2PA, "with_ai_metadata": withAI, "with_suspicious_text": suspiciousText, "actionable_files": actionable, "findings_by_confidence": confidenceCounts}
}

func printAuditHuman(report map[string]any) {
	summary, _ := report["summary"].(map[string]any)
	fmt.Printf("%s\n", cliText(
		fmt.Sprintf("Root: %v\nFiles scanned: %v\nFiles skipped: %d\nBy kind: %v\nWith C2PA: %v\nWith AI metadata: %v\nWith suspicious text: %v\nActionable files: %v\nFindings by confidence: %v", report["root"], report["files_scanned"], len(report["files_skipped"].([]map[string]any)), summary["by_kind"], summary["with_c2pa"], summary["with_ai_metadata"], summary["with_suspicious_text"], summary["actionable_files"], summary["findings_by_confidence"]),
		fmt.Sprintf("根目录：%v\n已扫描文件：%v\n已跳过文件：%d\n按类型：%v\n包含 C2PA：%v\n包含 AI 元数据：%v\n包含可疑文本：%v\n需处理文件：%v\n按置信度统计：%v", report["root"], report["files_scanned"], len(report["files_skipped"].([]map[string]any)), summary["by_kind"], summary["with_c2pa"], summary["with_ai_metadata"], summary["with_suspicious_text"], summary["actionable_files"], summary["findings_by_confidence"]),
	))
	for _, raw := range report["files"].([]map[string]any) {
		path := fmt.Sprint(raw["path"])
		findings, _ := raw["findings"].([]string)
		confidence, _ := raw["confidence"].([]string)
		for i, finding := range findings {
			level := ""
			if i < len(confidence) {
				level = confidence[i]
			}
			fmt.Printf("  [%s] %s: %s\n", level, path, finding)
		}
	}
	for _, raw := range report["files_skipped"].([]map[string]any) {
		fmt.Printf("  [%s] %v: %v\n", cliText("skipped", "已跳过"), raw["path"], localizedError(errors.New(fmt.Sprint(raw["reason"]))))
	}
}

func makeAuditSARIF(report map[string]any) map[string]any {
	results := []any{}
	root, _ := report["root"].(string)
	for _, raw := range report["files"].([]map[string]any) {
		path := fmt.Sprint(raw["path"])
		findings, _ := raw["findings"].([]string)
		confidence, _ := raw["confidence"].([]string)
		for i, finding := range findings {
			ruleID, level := "AI-WATERMARK-METADATA", "warning"
			low := strings.ToLower(finding)
			if strings.Contains(low, "c2pa") || strings.Contains(low, "jumb") || raw["has_c2pa"] == true {
				ruleID, level = "AI-WATERMARK-C2PA", "error"
			} else if strings.Contains(low, "layer-a") {
				ruleID = "AI-WATERMARK-UNICODE-LAYER-A"
			} else if strings.Contains(low, "stylometry") {
				ruleID, level = "AI-STYLES-HIGH-PROBABILITY", "note"
			} else if i < len(confidence) && confidence[i] == "informational" {
				level = "note"
			}
			uri := path
			if root != "" {
				if rel, err := filepath.Rel(root, path); err == nil {
					uri = filepath.ToSlash(rel)
				}
			}
			results = append(results, map[string]any{"ruleId": ruleID, "level": level, "message": map[string]string{"text": finding}, "locations": []any{map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]string{"uri": uri}}}}})
		}
	}
	return map[string]any{"$schema": "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json", "version": "2.1.0", "runs": []any{map[string]any{"tool": map[string]any{"driver": map[string]any{"name": "aiwr", "version": core.Version}}, "results": results}}}
}

func suspiciousReport(report core.FileReport) bool {
	if report.Text != nil && core.StylometrySuspicious(report.Text.Stylometry) {
		return true
	}
	if report.HasC2PA || report.HasAIMetadata || report.SuspiciousTotal > 0 {
		return true
	}
	for i, finding := range report.Findings {
		if finding == "" || strings.HasPrefix(strings.ToLower(finding), "no ") {
			continue
		}
		if i >= len(report.FindingsConfidence) || report.FindingsConfidence[i] != "informational" {
			return true
		}
	}
	return false
}

func countSuspicious(reports []core.FileReport) int {
	n := 0
	for _, report := range reports {
		if suspiciousReport(report) {
			n++
		}
	}
	return n
}

func suspiciousSuffix(value bool) string {
	if value {
		return " [" + cliText("suspicious", "可疑") + "]"
	}
	return ""
}

func printCleanHuman(result core.CleanResult) {
	status := cliText("unchanged", "未变化")
	if result.Changed {
		status = cliText("cleaned", "已清理")
	}
	fmt.Printf("%s\n", cliText(
		fmt.Sprintf("%s -> %s: %s (%s, %d -> %d bytes)", result.Input, result.Output, status, result.Kind, result.BytesIn, result.BytesOut),
		fmt.Sprintf("%s → %s：%s（%s，%d → %d 字节）", result.Input, result.Output, status, result.Kind, result.BytesIn, result.BytesOut),
	))
	for _, action := range result.Actions {
		fmt.Printf("  - %s\n", action)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(os.Stderr, "  %s：%s\n", cliText("warning", "警告"), warning)
	}
}

func printReportHuman(report core.FileReport, stylometry bool, threshold float64) {
	state := cliText("clean", "干净")
	if suspiciousReport(report) {
		state = cliText("suspicious", "可疑")
	}
	fmt.Printf("%s\n", cliText(
		fmt.Sprintf("%s: %s/%s (%s)", report.Path, report.Kind, report.Format, state),
		fmt.Sprintf("%s：%s/%s（%s）", report.Path, report.Kind, report.Format, state),
	))
	for i, finding := range report.Findings {
		confidence := ""
		if i < len(report.FindingsConfidence) {
			confidence = ", " + report.FindingsConfidence[i]
		}
		fmt.Printf("  - %s%s\n", finding, confidence)
	}
	if report.Text != nil {
		fmt.Printf("%s\n", cliText(
			fmt.Sprintf("  text: %d units, %d suspicious hits", report.Text.Length, report.Text.SuspiciousTotal),
			fmt.Sprintf("  文本：%d 个单元，%d 个可疑命中", report.Text.Length, report.Text.SuspiciousTotal),
		))
		for _, hit := range report.Text.Hits {
			fmt.Printf("  - %s %s x%d (%s, %s)\n", hit.Codepoint, hit.Kind, hit.Count, hit.Confidence, hit.Label)
		}
		if stylometry && report.Text.Stylometry != nil {
			printStylometryHuman(report.Text.Stylometry, threshold)
		}
	}
	for _, note := range report.Notes {
		fmt.Printf("  %s：%s\n", cliText("note", "说明"), note)
	}
}

func printStylometryHuman(values map[string]any, threshold float64) {
	printStylometryHumanWithExplain(values, threshold, false)
}

func printStylometryHumanWithExplain(values map[string]any, threshold float64, explain bool) {
	status, _ := values["status"].(string)
	words, _ := values["word_count"].(int)
	sentences, _ := values["sentence_count"].(int)
	score, hasScore := values["score"].(float64)
	confidence, _ := values["confidence_level"].(string)
	if hasScore {
		fmt.Printf("%s\n", cliText(
			fmt.Sprintf("  stylometry: %s, score %.3f / threshold %.3f, confidence %s (%d words, %d sentences)", status, score, threshold, confidence, words, sentences),
			fmt.Sprintf("  文体统计：%s，得分 %.3f / 阈值 %.3f，置信度 %s（%d 个词，%d 个句子）", status, score, threshold, confidence, words, sentences),
		))
	} else {
		fmt.Printf("%s\n", cliText(
			fmt.Sprintf("  stylometry: %s (%d words, %d sentences; score unavailable below calibration length)", status, words, sentences),
			fmt.Sprintf("  文体统计：%s（%d 个词，%d 个句子；文本短于校准长度，无法提供得分）", status, words, sentences),
		))
	}
	if findings, ok := values["findings"].([]string); ok {
		for _, finding := range findings {
			fmt.Printf("    - %s\n", finding)
		}
	}
	if explain {
		if markers, ok := values["matched_markers"].([]map[string]any); ok && len(markers) > 0 {
			fmt.Printf("\n  %s：\n", cliText("matched phrases detail", "命中短语详情"))
			for _, marker := range markers {
				fmt.Printf("    * %v（%s：%v，%s：%v）\n", marker["phrase"], cliText("occurrences", "出现次数"), marker["count"], cliText("weight", "权重"), marker["weight"])
				if samples, ok := marker["samples"].([]string); ok && len(samples) > 0 {
					fmt.Printf("      %s：%q\n", cliText("sample", "示例"), samples[0])
				}
			}
		}
	}
}

func runStylometry(args []string) int {
	parsed, err := parseCommon("score-stylometry", args, "text")
	if err != nil {
		return cliError(err)
	}
	parsed.opts.Stylometry = true
	if len(parsed.paths) == 0 || (len(parsed.paths) == 1 && parsed.paths[0] == "-") {
		data, readErr := readStdin()
		if readErr != nil {
			return cliError(readErr)
		}
		report, inspectErr := core.InspectBytes(data, "stdin.txt", parsed.opts)
		if inspectErr != nil {
			return cliError(inspectErr)
		}
		if parsed.opts.JSON {
			writeJSON(report.Text.Stylometry)
		} else {
			printStylometryHumanWithExplain(report.Text.Stylometry, parsed.opts.Threshold, parsed.explain)
		}
		if core.StylometrySuspiciousAt(report.Text.Stylometry, parsed.opts.Threshold) {
			return 1
		}
		return 0
	}

	values := []map[string]any{}
	errorsCount := 0
	for _, path := range parsed.paths {
		st, statErr := os.Stat(path)
		if statErr != nil {
			errorsCount++
			if !parsed.opts.JSON {
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(statErr))
			}
			continue
		}
		if st.IsDir() {
			reports, inspectErr := core.InspectDirectory(path, parsed.opts)
			if inspectErr != nil {
				errorsCount++
			}
			for _, report := range reports {
				if report.Text != nil && report.Text.Stylometry != nil {
					values = append(values, report.Text.Stylometry)
				}
			}
			continue
		}
		report, inspectErr := core.InspectFile(path, parsed.opts)
		if inspectErr != nil {
			errorsCount++
			if !parsed.opts.JSON {
				fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(inspectErr))
			}
			continue
		}
		if report.Text != nil && report.Text.Stylometry != nil {
			values = append(values, report.Text.Stylometry)
		}
	}
	if parsed.opts.JSON {
		if len(values) == 1 && errorsCount == 0 {
			writeJSON(values[0])
		} else {
			writeJSON(map[string]any{"results": values, "errors": errorsCount})
		}
	} else {
		for _, value := range values {
			printStylometryHuman(value, parsed.opts.Threshold)
		}
	}
	if errorsCount > 0 {
		return 3
	}
	for _, value := range values {
		if core.StylometrySuspiciousAt(value, parsed.opts.Threshold) {
			return 1
		}
	}
	return 0
}

func runGumbel(args []string) int {
	fs := newFlagSet("detect-gumbel")
	key := os.Getenv("WATERMARKS_GUMBEL_KEY")
	window := core.DefaultGumbelWindow
	threshold := core.DefaultGumbelThreshold
	tokens := false
	jsonOutput := false
	forceText := false
	fs.StringVar(&key, "key", key, "watermark key (prefer WATERMARKS_GUMBEL_KEY)")
	fs.IntVar(&window, "window", window, "context window size")
	fs.Float64Var(&threshold, "threshold", threshold, "p-value threshold")
	fs.BoolVar(&tokens, "tokens", false, "read token IDs as JSON array or one per line")
	fs.BoolVar(&jsonOutput, "json", false, "emit JSON")
	fs.BoolVar(&forceText, "force-text", false, "allow binary-looking text input")
	if err := fs.Parse(normalizeFlagArgs(args)); err != nil {
		return cliError(err)
	}
	if fs.NArg() > 1 {
		return cliError(errors.New("detect-gumbel accepts at most one input path"))
	}
	if strings.TrimSpace(key) == "" {
		return cliError(errors.New("no watermark key; pass --key or set WATERMARKS_GUMBEL_KEY"))
	}
	if window < 1 {
		return cliError(errors.New("--window must be at least 1"))
	}
	if threshold <= 0 || threshold >= 1 {
		return cliError(errors.New("--threshold must be in (0,1)"))
	}
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	data, err := readNamedInput(path)
	if err != nil {
		return cliError(err)
	}
	var report map[string]any
	if tokens {
		ids, loadErr := core.LoadGumbelTokenIDs(data)
		if loadErr != nil {
			return cliError(loadErr)
		}
		report, err = core.DetectGumbelTokenIDs(ids, key, window, threshold)
	} else {
		if !forceText {
			if why := binaryInputReason(data); why != "" {
				return cliError(fmt.Errorf("refusing to detect binary-looking input as text: %s; use --force-text", why))
			}
		}
		report, err = core.DetectGumbelText(string(data), key, window, threshold)
	}
	if err != nil {
		return cliError(err)
	}
	if jsonOutput {
		writeJSON(report)
	} else {
		watermarked, _ := report["is_watermarked"].(bool)
		pValue, _ := report["p_value"].(float64)
		counted, _ := report["counted"].(int)
		total, _ := report["tokens_total"].(int)
		fmt.Printf("%s\n", cliText(
			fmt.Sprintf("keyed-Gumbel (EXP) detection: watermarked=%t p=%.3g (threshold %g) counted=%d/%d", watermarked, pValue, threshold, counted, total),
			fmt.Sprintf("keyed-Gumbel（EXP）检测：含水印=%t，p=%.3g（阈值 %g），已计数=%d/%d", watermarked, pValue, threshold, counted, total),
		))
	}
	return 0
}

func readNamedInput(path string) ([]byte, error) {
	if path == "-" {
		return readStdin()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	maxBytes := core.MaxInputBytes()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return data, nil
}

func binaryInputReason(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) >= 4 && string(data[:4]) == "PK\x03\x04" {
		return "ZIP container"
	}
	control := 0
	for _, b := range data {
		if b == 0 || b < 9 || (b >= 14 && b < 32) {
			control++
		}
	}
	if float64(control)/float64(len(data)) > 0.02 {
		return "binary data"
	}
	return ""
}

func makeSARIF(reports []core.FileReport) map[string]any {
	results := []any{}
	for _, report := range reports {
		if !suspiciousReport(report) && report.Kind != core.KindUnknown {
			continue
		}
		message := "possible AI watermark/provenance marker"
		if len(report.Findings) > 0 {
			message = strings.Join(report.Findings, "; ")
		}
		results = append(results, map[string]any{
			"ruleId": "aiwr.suspicious-metadata", "level": "warning",
			"message":   map[string]string{"text": message},
			"locations": []any{map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]string{"uri": report.Path}}}},
		})
	}
	return map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json", "version": "2.1.0",
		"runs": []any{map[string]any{"tool": map[string]any{"driver": map[string]any{"name": "aiwr", "version": core.Version, "rules": []any{map[string]any{"id": "aiwr.suspicious-metadata", "shortDescription": map[string]string{"text": "Suspicious AI watermark or provenance metadata"}}}}}, "results": results}},
	}
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := os.Getenv("WATERMARKS_SERVER_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 8765
	if raw := strings.TrimSpace(os.Getenv("WATERMARKS_SERVER_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cliError(fmt.Errorf("invalid WATERMARKS_SERVER_PORT %q", raw))
		}
		port = parsed
	}
	apiKey := os.Getenv("WATERMARKS_SERVER_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("WATERMARKS_API_KEY")
	}
	strategyConfig := os.Getenv("WATERMARKS_CLEAN_STRATEGY_FILE")
	showVersion := false
	fs.StringVar(&host, "host", host, "listen host")
	fs.IntVar(&port, "port", port, "listen port")
	fs.StringVar(&apiKey, "api-key", apiKey, "Bearer API key (prefer an environment variable)")
	fs.StringVar(&strategyConfig, "strategy-config", strategyConfig, "Layer B default strategy JSON file")
	fs.BoolVar(&showVersion, "V", false, "print service version and exit")
	fs.BoolVar(&showVersion, "version", false, "alias for -V")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	if showVersion {
		fmt.Printf("aiwr %s\n", core.Version)
		return 0
	}
	if fs.NArg() != 0 {
		return cliError(errors.New("serve does not accept positional arguments"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Fprintf(os.Stderr, "%s\n", cliText(
		fmt.Sprintf("aiwr service listening on http://%s:%d", host, port),
		fmt.Sprintf("aiwr 服务正在监听 http://%s:%d", host, port),
	))
	if err := core.ServeWithOptions(ctx, host, port, apiKey, strategyConfig); err != nil {
		return cliError(err)
	}
	return 0
}

func readStdin() ([]byte, error) {
	maxBytes := core.MaxStdinBytes()
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("stdin exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func writeJSON(value any) {
	writeJSONTo(os.Stdout, value)
}

func writeJSONTo(writer io.Writer, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: cannot encode JSON: %v\n", err)
		return
	}
	data = append(data, '\n')
	_, _ = writer.Write(data)
}

func cliError(err error) int {
	return writeCLIError(err)
}

// The standard flag package stops parsing at the first positional argument,
// while the upstream scripts accept options in either order. Move known
// option tokens (and their values) before positional paths so both forms work.
func normalizeFlagArgs(args []string) []string {
	valueFlags := map[string]bool{
		"-o": true, "--output": true, "--output-dir": true, "--as": true,
		"--deep-images": true, "--reflink": true, "--jobs": true, "-j": true,
		"--audio-tempo": true, "--audio-pitch": true, "--audio-bitrate": true,
		"--tempo": true, "--pitch-semitones": true, "--reencode-bitrate": true, "--codec": true,
		"--ffmpeg-timeout": true, "--threshold": true, "--provider": true,
		"--model": true, "--base-url": true, "--api-key": true, "--prompt": true,
		"--temperature": true, "--host": true, "--port": true, "--key": true, "--window": true,
		"--backend": true, "--tactic": true, "--strategy": true, "--style": true,
		"--rewrite-level": true, "--candidates": true, "--max-loops": true,
		"--gumbel-key": true, "--lang": true, "--original-lang": true, "--select": true,
		"--format": true, "--skip": true,
		"--upstream-scripts": true, "--upstream-dir-scripts": true, "--synthid-dir": true,
		"--remove-pixel": true, "--ctrlregen-dir": true, "--ctrlregen-intensity": true,
		"--ctrlregen-steps": true, "--ctrlregen-device": true, "--ctrlregen-seed": true,
		"--ctrlregen-timeout": true, "--markdiffusion-dir": true, "--markdiffusion-intensity": true,
		"--markdiffusion-model": true, "--markdiffusion-size": true, "--markdiffusion-steps": true,
		"--markdiffusion-device": true, "--markdiffusion-timeout": true, "--vote-threshold": true,
		"--frame-fraction": true, "--reasoning-effort": true, "--markllm-scheme": true,
		"--markllm-dir": true, "--markllm-model": true, "--markllm-timeout": true,
		"--target-margin": true, "--noop-lex-floor": true,
	}
	flags := []string{}
	paths := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			paths = append(paths, args[i+1:]...)
			break
		}
		if arg == "-" {
			paths = append(paths, arg)
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && valueFlags[arg] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		paths = append(paths, arg)
	}
	return append(flags, paths...)
}
