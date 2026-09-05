package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/iruanp/aiwr/internal/core"
	"github.com/iruanp/aiwr/internal/stealer"
)

const stealerUsage = `aiwr stealer - black-box watermark stealing helpers

Usage:
  aiwr stealer query [options]
  aiwr stealer build [options]
  aiwr stealer detect [options]
  aiwr download-prompts [options]

The query step is offline by default (dry-run). The openai-compatible backend
requires an explicit remote opt-in; build and detect are model-free.
`

func runStealer(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, stealerUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "query":
		return runStealerQuery(args[1:])
	case "build":
		return runStealerBuild(args[1:])
	case "detect":
		return runStealerDetect(args[1:])
	default:
		return cliError(fmt.Errorf("unknown stealer command %q\n\n%s", args[0], stealerUsage))
	}
}

func runStealerQuery(args []string) int {
	backend := os.Getenv("WATERMARKS_STEAL_BACKEND")
	if backend == "" {
		backend = "dry-run"
	}
	baseURL := os.Getenv("WATERMARKS_STEAL_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := os.Getenv("WATERMARKS_STEAL_MODEL")
	apiKey := os.Getenv("WATERMARKS_STEAL_API_KEY")
	allowRemote := stealerEnvBool("WATERMARKS_STEAL_ALLOW_REMOTE")
	prompts, out := "", ""
	maxNewTokens, concurrency := 512, 1
	fs := newFlagSet("stealer query")
	fs.StringVar(&prompts, "prompts", prompts, "prompt corpus (JSONL or plain lines)")
	fs.StringVar(&out, "out", out, "JSONL of prompt/reply rows")
	fs.StringVar(&backend, "backend", backend, "query backend: dry-run or openai-compatible")
	fs.StringVar(&baseURL, "base-url", baseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&model, "model", model, "model name (or WATERMARKS_STEAL_MODEL)")
	fs.StringVar(&apiKey, "api-key", apiKey, "API key (or WATERMARKS_STEAL_API_KEY)")
	fs.IntVar(&maxNewTokens, "max-new-tokens", maxNewTokens, "maximum generated tokens")
	fs.IntVar(&concurrency, "concurrency", concurrency, "parallel model requests")
	fs.BoolVar(&allowRemote, "allow-remote", allowRemote, "allow non-loopback model endpoints")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	if fs.NArg() != 0 {
		return cliError(errors.New("stealer query does not accept positional arguments"))
	}
	if strings.TrimSpace(prompts) == "" || strings.TrimSpace(out) == "" {
		return cliError(errors.New("stealer query requires --prompts and --out"))
	}
	err := stealer.Query(stealer.QueryOptions{
		Prompts: prompts, Out: out, Backend: backend, BaseURL: baseURL, Model: model,
		APIKey: apiKey, MaxNewTokens: maxNewTokens, Concurrency: concurrency,
		AllowRemote: allowRemote, Progress: os.Stdout,
	})
	if err != nil {
		return stealerOperationalError(err)
	}
	return 0
}

func runStealerBuild(args []string) int {
	repliesPath, baselinePath, out := "", "", ""
	contextLen, topK, minContext := 8, 50, 1
	alpha := 0.4
	fs := newFlagSet("stealer build")
	fs.StringVar(&repliesPath, "replies", repliesPath, "watermarked replies (JSONL)")
	fs.StringVar(&baselinePath, "baseline", baselinePath, "optional non-watermarked baseline replies (JSONL)")
	fs.IntVar(&contextLen, "ctx", contextLen, "context length in tokens")
	fs.IntVar(&topK, "topk", topK, "tokens retained per context")
	fs.Float64Var(&alpha, "alpha", alpha, "add-alpha smoothing")
	fs.IntVar(&minContext, "min-context", minContext, "minimum context occurrences")
	fs.StringVar(&out, "out", out, "output s-star JSON")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	if fs.NArg() != 0 {
		return cliError(errors.New("stealer build does not accept positional arguments"))
	}
	if strings.TrimSpace(repliesPath) == "" || strings.TrimSpace(out) == "" {
		return cliError(errors.New("stealer build requires --replies and --out"))
	}
	if contextLen < 0 || topK < 0 || minContext < 0 || alpha < 0 || math.IsNaN(alpha) || math.IsInf(alpha, 0) {
		return cliError(errors.New("--ctx, --topk, and --min-context must be non-negative and --alpha must be finite and non-negative"))
	}
	replies, err := stealer.ReadReplies(repliesPath)
	if err != nil {
		return stealerOperationalError(err)
	}
	if len(replies) == 0 {
		return stealerOperationalError(fmt.Errorf("no watermarked replies read from %s", repliesPath))
	}
	baseline := []string{}
	if baselinePath != "" {
		baseline, err = stealer.ReadReplies(baselinePath)
		if err != nil {
			return stealerOperationalError(err)
		}
	}
	fmt.Printf("building s* from %d watermarked replies, %d baseline replies\n", len(replies), len(baseline))
	if len(baseline) == 0 {
		fmt.Fprintln(os.Stderr, "  note: no baseline supplied -> unigram fallback only")
	}
	watermarkedCounts := stealer.CountNGrams(replies, contextLen)
	baselineCounts := stealer.CountNGrams(baseline, contextLen)
	built := stealer.BuildScorer(watermarkedCounts, baselineCounts, contextLen, topK, alpha, minContext)
	data, err := json.MarshalIndent(built, "", "  ")
	if err != nil {
		return stealerOperationalError(err)
	}
	data = append(data, '\n')
	if err := core.WriteFileAtomic(out, data, 0o644); err != nil {
		return stealerOperationalError(err)
	}
	contexts := 0
	if table, ok := built["scorer"].(map[string]any); ok {
		contexts = len(table)
	}
	fmt.Printf("wrote %s (%d contexts, top-%d)\n", out, contexts, topK)
	return 0
}

func runStealerDetect(args []string) int {
	textInput, filePath, scorerPath := "", "", ""
	contextLen := 0
	textSet, fileSet := false, false
	fs := newFlagSet("stealer detect")
	fs.StringVar(&textInput, "text", textInput, "text to score")
	fs.StringVar(&filePath, "file", filePath, "read candidate text from a file")
	fs.StringVar(&scorerPath, "s-star", scorerPath, "saved s-star JSON")
	fs.IntVar(&contextLen, "ctx", contextLen, "override context length (0 uses the scorer value)")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "text":
			textSet = true
		case "file":
			fileSet = true
		}
	})
	if fs.NArg() != 0 {
		return cliError(errors.New("stealer detect does not accept positional arguments"))
	}
	if !textSet && !fileSet {
		return cliError(errors.New("stealer detect requires --text or --file"))
	}
	if textSet && fileSet {
		return cliError(errors.New("stealer detect accepts only one of --text or --file"))
	}
	if strings.TrimSpace(scorerPath) == "" {
		return cliError(errors.New("stealer detect requires --s-star"))
	}
	if contextLen < 0 {
		return cliError(errors.New("--ctx must be non-negative"))
	}
	scorer, err := stealer.LoadScorer(scorerPath)
	if err != nil {
		return stealerOperationalError(err)
	}
	candidate := textInput
	if fileSet {
		data, readErr := readNamedInput(filePath)
		if readErr != nil {
			return stealerOperationalError(readErr)
		}
		candidate = string(data)
	}
	if contextLen == 0 {
		contextLen = stealer.ScorerContextLength(scorer)
	}
	tokens := stealer.DefaultTokenize(candidate)
	result := stealer.ScoreSequence(scorer, tokens, contextLen)
	result["tokens"] = len(tokens)
	mean := 0.0
	if applied, ok := result["applied"].(int); ok && applied > 0 {
		if score, ok := result["score"].(float64); ok {
			mean = math.Round(score/float64(applied)*100000) / 100000
		}
	}
	result["mean"] = mean
	writeJSON(result)
	return 0
}

func runDownloadPrompts(args []string) int {
	dataset, config, split := "allenai/c4", "realnewslike", "train"
	count, minChars, maxChars, offset := 30000, 0, 0, 0
	field, out := "text", ""
	baseURL := os.Getenv("WATERMARKS_STEAL_DATASETS_BASE_URL")
	if baseURL == "" {
		baseURL = "https://datasets-server.huggingface.co"
	}
	delaySeconds := 1.5
	startOver := false
	fs := newFlagSet("download-prompts")
	fs.StringVar(&dataset, "dataset", dataset, "Hugging Face dataset id")
	fs.StringVar(&config, "config", config, "dataset config/subset")
	fs.StringVar(&split, "split", split, "dataset split")
	fs.IntVar(&count, "count", count, "number of prompts to keep")
	fs.StringVar(&field, "field", field, "row field used as the prompt")
	fs.StringVar(&out, "out", out, "output directory (default: ./stealer/prompts)")
	fs.StringVar(&baseURL, "base-url", baseURL, "datasets-server base URL")
	fs.Float64Var(&delaySeconds, "delay", delaySeconds, "seconds between page requests")
	fs.IntVar(&minChars, "min-chars", minChars, "drop prompts shorter than this")
	fs.IntVar(&maxChars, "max-chars", maxChars, "truncate prompts longer than this")
	fs.IntVar(&offset, "offset", offset, "dataset row offset")
	fs.BoolVar(&startOver, "start-over", startOver, "ignore resume state")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	if fs.NArg() != 0 {
		return cliError(errors.New("download-prompts does not accept positional arguments"))
	}
	if math.IsNaN(delaySeconds) || math.IsInf(delaySeconds, 0) || delaySeconds < 0 {
		return cliError(errors.New("--delay must be finite and non-negative"))
	}
	delay := time.Duration(delaySeconds * float64(time.Second))
	if delay < 0 {
		return cliError(errors.New("--delay is too large"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	written, err := stealer.DownloadPrompts(stealer.DownloadOptions{
		Dataset: dataset, Config: config, Split: split, Count: count, Field: field,
		Out: out, BaseURL: baseURL, Delay: delay, MinChars: minChars, MaxChars: maxChars,
		Offset: offset, StartOver: startOver, Context: ctx, Progress: os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %v\n", err)
		if errors.Is(err, stealer.ErrInterrupted) {
			return 130
		}
		if strings.Contains(strings.ToLower(err.Error()), "dataset exhausted") {
			return 2
		}
		return 1
	}
	if written < count {
		return 2
	}
	return 0
}

func stealerEnvBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func stealerOperationalError(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %v\n", err)
	}
	return 1
}
