package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/iruanp/aiwr/internal/core"
)

const defaultRewritePrompt = `Rewrite the following text in natural, fluent language while preserving its meaning, facts, structure, and approximate length. Do not mention this instruction. Do not add a preface or explanation. Return only the rewritten text. Avoid unusual invisible characters and do not use zero-width or bidi controls.`

type rewriteMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model           string           `json:"model"`
	Messages        []rewriteMessage `json:"messages"`
	Temperature     float64          `json:"temperature,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	Stream          bool             `json:"stream,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message rewriteMessage `json:"message"`
	} `json:"choices"`
	Message rewriteMessage `json:"message"`
}

func runRewriteText(args []string) int {
	providerDefault := os.Getenv("AIWR_REWRITE_PROVIDER")
	if providerDefault == "" {
		providerDefault = os.Getenv("WATERMARKS_REWRITE_BACKEND")
	}
	if providerDefault == "" {
		providerDefault = "print-prompt"
	}
	modelDefault := os.Getenv("AIWR_REWRITE_MODEL")
	if modelDefault == "" {
		modelDefault = "gpt-4o-mini"
	}
	baseDefault := os.Getenv("AIWR_REWRITE_BASE_URL")
	if baseDefault == "" {
		baseDefault = "https://api.openai.com/v1/chat/completions"
	}
	keyDefault := os.Getenv("OPENAI_API_KEY")
	fs := newFlagSet("rewrite-text")
	provider := providerDefault
	model := modelDefault
	baseURL := baseDefault
	apiKey := keyDefault
	prompt := defaultRewritePrompt
	temperature := 0.2
	output := ""
	promptOnly := false
	dryRun := false
	inPlace := false
	allowRemote := false
	jsonStats := false
	noLayerA := false
	nfkc := false
	aggressive := false
	noSpaces := false
	stripEmoji := false
	stripBidi := false
	tactic := "paraphrase"
	strategy := ""
	upstreamScripts := ""
	style := ""
	rewriteLevel := -1.0
	candidates := 1
	maxLoops := 1
	gumbelKey := os.Getenv("WATERMARKS_GUMBEL_KEY")
	lang := "French"
	originalLang := "English"
	selection := "min-divergence"
	reasoningEffort := os.Getenv("WATERMARKS_REWRITE_REASONING_EFFORT")
	if reasoningEffort == "" {
		reasoningEffort = "none"
	}
	markllmScheme := os.Getenv("WATERMARKS_MARKLLM_SCHEME")
	markllmDir := os.Getenv("MARKLLM_DIR")
	markllmModel := os.Getenv("MARKLLM_MODEL")
	if markllmModel == "" {
		markllmModel = "facebook/opt-1.3b"
	}
	markllmTimeout := 180
	if raw := os.Getenv("WATERMARKS_MARKLLM_TIMEOUT"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			markllmTimeout = parsed
		}
	}
	targetMargin := 0.0
	chunkShuffle := false
	noopLexFloor := 0.05
	fs.StringVar(&provider, "provider", provider, "rewrite provider: none, openai, openai-compatible, ollama")
	fs.StringVar(&provider, "backend", provider, "alias for --provider; also accepts print-prompt")
	fs.StringVar(&model, "model", model, "model name")
	fs.StringVar(&baseURL, "base-url", baseURL, "OpenAI-compatible chat endpoint")
	fs.StringVar(&apiKey, "api-key", apiKey, "API key (prefer OPENAI_API_KEY)")
	fs.StringVar(&prompt, "prompt", prompt, "rewriting instruction")
	fs.Float64Var(&temperature, "temperature", temperature, "sampling temperature")
	fs.StringVar(&output, "o", "", "output file (default: stdout for stdin, NAME.rewritten.EXT for a file)")
	fs.StringVar(&output, "output", "", "output file")
	fs.BoolVar(&promptOnly, "prompt-only", false, "print the Layer B prompt and do not call a provider")
	fs.BoolVar(&dryRun, "dry-run", false, "alias for --prompt-only")
	fs.BoolVar(&inPlace, "in-place", false, "replace a file after creating FILE.bak")
	fs.BoolVar(&allowRemote, "allow-remote", false, "allow non-loopback rewrite endpoints")
	fs.BoolVar(&jsonStats, "json-stats", false, "emit rewrite statistics as JSON")
	fs.BoolVar(&noLayerA, "no-layer-a", false, "skip deterministic Layer A cleanup before/after rewriting")
	fs.BoolVar(&nfkc, "nfkc", false, "apply Unicode NFKC normalization in Layer A")
	fs.BoolVar(&aggressive, "aggressive-homoglyphs", false, "normalize selected homoglyphs in Layer A")
	fs.BoolVar(&noSpaces, "no-normalize-spaces", false, "preserve space-like characters in Layer A")
	fs.BoolVar(&stripEmoji, "strip-emoji-glue", false, "strip emoji joiners in Layer A")
	fs.BoolVar(&stripBidi, "strip-bidi", false, "strip bidi controls in Layer A")
	fs.StringVar(&tactic, "tactic", tactic, "rewrite tactic: paraphrase, backtranslate, structural, humanize, code, chunk, mlm")
	fs.StringVar(&strategy, "strategy", strategy, "ordered tactic@intensity steps")
	fs.StringVar(&upstreamScripts, "upstream-scripts", upstreamScripts, "optional upstream service/scripts directory (enables the mlm tactic)")
	fs.StringVar(&upstreamScripts, "upstream-dir-scripts", upstreamScripts, "alias for --upstream-scripts")
	fs.StringVar(&style, "style", style, "optional writing-style instruction")
	fs.Float64Var(&rewriteLevel, "rewrite-level", rewriteLevel, "rewrite intensity in (0,1]")
	fs.IntVar(&candidates, "candidates", candidates, "variants per evaluation loop")
	fs.IntVar(&maxLoops, "max-loops", maxLoops, "maximum evaluation loops")
	fs.StringVar(&gumbelKey, "gumbel-key", gumbelKey, "same-key Gumbel detector key (prefer WATERMARKS_GUMBEL_KEY)")
	fs.StringVar(&lang, "lang", lang, "pivot language for backtranslate")
	fs.StringVar(&originalLang, "original-lang", originalLang, "source language")
	fs.StringVar(&selection, "select", selection, "candidate selection: min-divergence or max-margin")
	fs.StringVar(&reasoningEffort, "reasoning-effort", reasoningEffort, "OpenAI-compatible reasoning effort: none, low, medium, high, or off")
	fs.StringVar(&markllmScheme, "markllm-scheme", markllmScheme, "MarkLLM scheme: kgw, synthid, synthid-text, exp, unigram, or sir")
	fs.StringVar(&markllmDir, "markllm-dir", markllmDir, "MarkLLM checkout root")
	fs.StringVar(&markllmModel, "markllm-model", markllmModel, "MarkLLM scoring model")
	fs.IntVar(&markllmTimeout, "markllm-timeout", markllmTimeout, "MarkLLM detection timeout in seconds")
	fs.Float64Var(&targetMargin, "target-margin", targetMargin, "minimum detector margin below threshold")
	fs.BoolVar(&chunkShuffle, "chunk-shuffle", chunkShuffle, "shuffle rewritten chunks when using the chunk tactic")
	fs.Float64Var(&noopLexFloor, "noop-lex-floor", noopLexFloor, "flag rewrites whose lexical divergence is below this floor; 0 disables")
	if err := fs.Parse(normalizeFlagArgs(args)); err != nil {
		return cliError(err)
	}
	if fs.NArg() > 1 {
		return cliError(errors.New("rewrite-text accepts at most one input path"))
	}
	if temperature < 0 || temperature > 2 {
		return cliError(errors.New("--temperature must be between 0 and 2"))
	}
	if candidates < 1 || maxLoops < 1 {
		return cliError(errors.New("--candidates and --max-loops must be at least 1"))
	}
	if rewriteLevel != -1 && (rewriteLevel <= 0 || rewriteLevel > 1) {
		return cliError(errors.New("--rewrite-level must be in (0,1] when set"))
	}
	if selection != "min-divergence" && selection != "max-margin" {
		return cliError(errors.New("--select must be min-divergence or max-margin"))
	}
	if reasoningEffort != "none" && reasoningEffort != "low" && reasoningEffort != "medium" && reasoningEffort != "high" && reasoningEffort != "off" {
		return cliError(errors.New("--reasoning-effort must be none, low, medium, high, or off"))
	}
	if markllmScheme != "" {
		switch strings.ToLower(markllmScheme) {
		case "kgw", "synthid", "synthid-text", "exp", "unigram", "sir":
		default:
			return cliError(fmt.Errorf("invalid --markllm-scheme %q", markllmScheme))
		}
		markllmScheme = strings.ToLower(markllmScheme)
	}
	if markllmTimeout < 1 {
		return cliError(errors.New("--markllm-timeout must be at least 1"))
	}
	if targetMargin < 0 {
		return cliError(errors.New("--target-margin must be non-negative"))
	}
	if noopLexFloor < 0 || noopLexFloor > 1 {
		return cliError(errors.New("--noop-lex-floor must be in [0,1]"))
	}
	if strategy != "" {
		if _, err := parseRewriteStrategy(strategy); err != nil {
			return cliError(err)
		}
	}
	if !knownRewriteTactic(tactic) {
		return cliError(fmt.Errorf("unknown --tactic %q", tactic))
	}
	inputName := "stdin.txt"
	var input []byte
	var err error
	if fs.NArg() == 0 || fs.Arg(0) == "-" {
		input, err = readStdin()
	} else {
		inputName = fs.Arg(0)
		input, err = os.ReadFile(inputName)
	}
	if err != nil {
		return cliError(err)
	}
	opts := core.DefaultOptions()
	opts.ForceType, opts.ForceText = "text", true
	opts.NFKC, opts.AggressiveHomoglyphs = nfkc, aggressive
	opts.NormalizeSpaces = !noSpaces
	opts.StripEmojiGlue, opts.StripBidi = stripEmoji, stripBidi
	opts.UpstreamScriptsDir = upstreamScripts
	opts.RewriteAPIKey = apiKey
	opts.RewriteModel = model
	opts.RewriteBaseURL = baseURL
	opts.RewriteStyle = style
	opts.RewriteTemperature = temperature
	opts.RewriteTimeoutSeconds = 120
	opts.RewriteAllowRemote = allowRemote
	opts.RewriteReasoningEffort = reasoningEffort
	opts.RewriteGumbelKey = gumbelKey
	opts.RewriteTargetMargin = targetMargin
	opts.RewriteChunkShuffle = chunkShuffle
	opts.RewriteNoopLexFloor = noopLexFloor
	opts.MarkLLMScheme = markllmScheme
	opts.MarkLLMDir = markllmDir
	opts.MarkLLMModel = markllmModel
	opts.MarkLLMTimeout = markllmTimeout
	layerA := input
	if !noLayerA {
		_, layerA, err = core.CleanBytes(input, inputName, opts)
		if err != nil {
			return cliError(err)
		}
	}
	instruction := buildTacticInstruction(prompt, tactic, style, rewriteLevel, lang, originalLang)
	fullPrompt := instruction + "\n\n--- INPUT ---\n" + string(layerA) + "\n--- END INPUT ---"
	if promptOnly || dryRun {
		fmt.Println(fullPrompt)
		return 0
	}
	externalStrategy := strategy
	if externalStrategy == "" && strings.EqualFold(tactic, "mlm") {
		level := rewriteLevel
		if level <= 0 {
			level = 0.3
		}
		externalStrategy = fmt.Sprintf("mlm@%g", level)
	}
	if externalStrategy == "" && markllmScheme != "" {
		level := rewriteLevel
		if level <= 0 {
			level = 1
		}
		externalStrategy = fmt.Sprintf("%s@%g", strings.ToLower(strings.TrimSpace(tactic)), level)
	}
	if strategyHasTactic(externalStrategy, "mlm") || markllmScheme != "" {
		opts.Strategy = externalStrategy
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "", "none", "print-prompt":
			opts.RewriteBackend = "print-prompt"
		case "openai", "openai-compatible", "openai_compatible":
			opts.RewriteBackend = "openai-compatible"
		case "ollama":
			opts.RewriteBackend = "ollama"
		default:
			return cliError(fmt.Errorf("unknown rewrite provider %q", provider))
		}
		rewritten, stats, externalErr := core.ApplyLayerB(string(layerA), opts)
		if externalErr != nil {
			return cliError(externalErr)
		}
		final := []byte(rewritten)
		if !noLayerA {
			_, final, err = core.CleanBytes(final, inputName, opts)
			if err != nil {
				return cliError(err)
			}
		}
		if stats == nil {
			stats = map[string]any{}
		}
		stats["provider"] = provider
		stats["model"] = model
		stats["layer_a"] = !noLayerA
		return writeRewriteOutput(inputName, final, output, inPlace, stats, jsonStats)
	}
	if strings.EqualFold(provider, "print-prompt") {
		fmt.Println(fullPrompt)
		return 0
	}
	if strings.EqualFold(provider, "none") {
		// A no-provider invocation remains useful as a deterministic Layer A
		// rewrite and never attempts network access implicitly.
		return writeRewriteOutput(inputName, layerA, output, inPlace, map[string]any{"provider": "none", "layer_a_only": true}, jsonStats)
	}
	if !allowRemote && !rewriteEndpointIsLoopback(baseURL, provider) {
		return cliError(errors.New("remote rewrite endpoint denied; pass --allow-remote explicitly"))
	}
	rewritten, stats, err := rewriteCandidates(string(layerA), instruction, provider, baseURL, apiKey, model, temperature, candidates, maxLoops, tactic, strategy, gumbelKey, selection, lang, originalLang, style, allowRemote, reasoningEffort, chunkShuffle, noopLexFloor, targetMargin)
	if err != nil {
		return cliError(err)
	}
	final := []byte(rewritten)
	if !noLayerA {
		_, final, err = core.CleanBytes(final, inputName, opts)
		if err != nil {
			return cliError(err)
		}
	}
	stats["provider"] = provider
	stats["model"] = model
	stats["layer_a"] = !noLayerA
	return writeRewriteOutput(inputName, final, output, inPlace, stats, jsonStats)
}

var rewriteTactics = map[string]bool{
	"paraphrase": true, "backtranslate": true, "structural": true,
	"humanize": true, "code": true, "chunk": true, "mlm": true,
}

func strategyHasTactic(spec, wanted string) bool {
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if at := strings.IndexByte(item, '@'); at > 0 && strings.EqualFold(strings.TrimSpace(item[:at]), wanted) {
			return true
		}
	}
	return false
}

type rewriteStep struct {
	tactic string
	level  float64
}

func knownRewriteTactic(tactic string) bool {
	return rewriteTactics[strings.ToLower(strings.TrimSpace(tactic))]
}

func buildTacticInstruction(prompt, tactic, style string, level float64, lang, originalLang string) string {
	if prompt == "" || prompt == defaultRewritePrompt {
		switch strings.ToLower(strings.TrimSpace(tactic)) {
		case "backtranslate":
			prompt = fmt.Sprintf("Translate the text to %s and then naturally translate it back to %s. Preserve facts, meaning, structure, and approximate length. Return only the final rewritten text.", lang, originalLang)
		case "structural":
			prompt = "Rewrite the text with natural variation in sentence structure and paragraph flow while preserving facts, meaning, and approximate length. Return only the rewritten text."
		case "humanize":
			prompt = "Rewrite the text in a natural human editorial voice. Preserve facts, meaning, and approximate length. Use straight quotes, avoid em dashes and filler phrasing, and return only the rewritten text."
		case "code":
			prompt = "Rewrite the code or technical text while preserving behavior, facts, syntax, and formatting. Return only the rewritten content."
		case "chunk":
			prompt = "Rewrite the text in coherent local chunks while preserving facts, meaning, ordering, and approximate length. Return only the rewritten text."
		default:
			prompt = defaultRewritePrompt
		}
	}
	if style != "" {
		prompt += "\nWriting style request: " + style
	}
	if level >= 0 {
		prompt += fmt.Sprintf("\nChange roughly %.0f%% of the wording while preserving meaning.", level*100)
	}
	return prompt
}

func parseRewriteStrategy(spec string) ([]rewriteStep, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, errors.New("--strategy must be a non-empty comma-separated tactic@intensity list")
	}
	steps := []rewriteStep{}
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		at := strings.LastIndexByte(item, '@')
		if at <= 0 || at == len(item)-1 {
			return nil, fmt.Errorf("bad strategy step %q; expected tactic@intensity", item)
		}
		tactic := strings.ToLower(strings.TrimSpace(item[:at]))
		if !knownRewriteTactic(tactic) {
			return nil, fmt.Errorf("unknown strategy tactic %q", tactic)
		}
		level, err := strconv.ParseFloat(strings.TrimSpace(item[at+1:]), 64)
		if err != nil || level <= 0 || level > 1 {
			return nil, fmt.Errorf("strategy intensity must be in (0,1], got %q", strings.TrimSpace(item[at+1:]))
		}
		steps = append(steps, rewriteStep{tactic: tactic, level: level})
	}
	return steps, nil
}

func rewriteCandidates(input, instruction, provider, baseURL, apiKey, model string, temperature float64, candidates, maxLoops int, tactic, strategy, gumbelKey, selection, lang, originalLang, style string, allowRemote bool, reasoningEffort string, chunkShuffle bool, noopLexFloor, targetMargin float64) (string, map[string]any, error) {
	var steps []rewriteStep
	var err error
	if strategy != "" {
		steps, err = parseRewriteStrategy(strategy)
		if err != nil {
			return "", nil, err
		}
	}
	type attempt struct {
		text       string
		divergence float64
		gumbel     map[string]any
	}
	attempts := []attempt{}
	passed := false
	loopsUsed := 0
	for loop := 0; loop < maxLoops && !passed; loop++ {
		loopsUsed++
		for candidate := 0; candidate < candidates; candidate++ {
			current := input
			if len(steps) > 0 {
				for _, step := range steps {
					stepInstruction := buildTacticInstruction("", step.tactic, style, step.level, lang, originalLang)
					current, err = callRewriteProvider(provider, baseURL, apiKey, model, stepInstruction, current, temperature, allowRemote, reasoningEffort)
					if err != nil {
						return "", nil, err
					}
					if step.tactic == "humanize" {
						current = core.HumanizeText(current)
					}
				}
			} else if strings.EqualFold(strings.TrimSpace(tactic), "chunk") {
				current, err = rewriteChunkCandidate(input, provider, baseURL, apiKey, model, temperature, lang, originalLang, style, allowRemote, reasoningEffort, chunkShuffle)
				if err != nil {
					return "", nil, err
				}
			} else {
				current, err = callRewriteProvider(provider, baseURL, apiKey, model, instruction, current, temperature, allowRemote, reasoningEffort)
				if err != nil {
					return "", nil, err
				}
				if tactic == "humanize" {
					current = core.HumanizeText(current)
				}
			}
			rec := attempt{text: current, divergence: bigramDivergence(input, current)}
			if strings.TrimSpace(gumbelKey) != "" {
				rec.gumbel, err = core.DetectGumbelText(current, gumbelKey, core.DefaultGumbelWindow, core.DefaultGumbelThreshold)
				if err != nil {
					return "", nil, err
				}
				if watermarked, _ := rec.gumbel["is_watermarked"].(bool); !watermarked {
					passed = true
				}
			}
			attempts = append(attempts, rec)
			if passed {
				break
			}
		}
	}
	if len(attempts) == 0 {
		return "", nil, errors.New("rewrite produced no candidate")
	}
	sort.SliceStable(attempts, func(i, j int) bool {
		if len(steps) == 0 && strings.TrimSpace(gumbelKey) == "" {
			return attempts[i].divergence > attempts[j].divergence
		}
		if selection == "max-margin" && attempts[i].gumbel != nil && attempts[j].gumbel != nil {
			left, _ := attempts[i].gumbel["score"].(float64)
			right, _ := attempts[j].gumbel["score"].(float64)
			return left < right
		}
		return attempts[i].divergence < attempts[j].divergence
	})
	selected := attempts[0]
	stats := map[string]any{
		"tactic": tactic, "strategy": strategy, "attempts": len(attempts),
		"candidates": candidates, "loops": loopsUsed, "passed": passed,
		"selection": selection, "lexical_divergence": selected.divergence,
		"target_margin": targetMargin, "chunk_shuffle": chunkShuffle,
	}
	stats["noop"] = noopLexFloor > 0 && selected.divergence < noopLexFloor
	stats["noop_lex_floor"] = noopLexFloor
	if selected.gumbel != nil {
		stats["gumbel"] = map[string]any{
			"before": func() map[string]any {
				report, _ := core.DetectGumbelText(input, gumbelKey, core.DefaultGumbelWindow, core.DefaultGumbelThreshold)
				return report
			}(),
			"after":   selected.gumbel,
			"cleared": passed,
		}
	}
	return selected.text, stats, nil
}

type rewriteUnit struct {
	text string
	sep  string
}

func splitRewriteUnits(text string) []rewriteUnit {
	units := []rewriteUnit{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '.' && text[i] != '!' && text[i] != '?' && text[i] != '\n' {
			continue
		}
		end := i + 1
		for end < len(text) && (text[end] == ' ' || text[end] == '\t' || text[end] == '\r' || text[end] == '\n') {
			end++
		}
		if end == i+1 && text[i] != '\n' {
			continue
		}
		unit := strings.TrimSpace(text[start : i+1])
		sep := text[i+1 : end]
		if unit != "" || strings.Contains(sep, "\n") {
			units = append(units, rewriteUnit{text: unit, sep: sep})
		}
		start = end
		i = end - 1
	}
	if start < len(text) {
		unit := strings.TrimSpace(text[start:])
		if unit != "" {
			units = append(units, rewriteUnit{text: unit})
		}
	}
	if len(units) == 0 && strings.TrimSpace(text) != "" {
		return []rewriteUnit{{text: strings.TrimSpace(text)}}
	}
	return units
}

func rewriteChunkCandidate(input, provider, baseURL, apiKey, model string, temperature float64, lang, originalLang, style string, allowRemote bool, reasoningEffort string, shuffle bool) (string, error) {
	units := splitRewriteUnits(input)
	if len(units) == 0 {
		return input, nil
	}
	if shuffle {
		values := make([]rewriteUnit, 0, len(units))
		for _, unit := range units {
			if unit.text != "" {
				values = append(values, rewriteUnit{text: unit.text})
			}
		}
		rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })
		units = values
	}
	var out strings.Builder
	for _, unit := range units {
		if unit.text == "" {
			out.WriteString(unit.sep)
			continue
		}
		prompt := buildTacticInstruction("", "chunk", style, -1, lang, originalLang)
		rewritten, err := callRewriteProvider(provider, baseURL, apiKey, model, prompt, unit.text, temperature, allowRemote, reasoningEffort)
		if err != nil {
			return "", err
		}
		out.WriteString(rewritten)
		if !shuffle {
			out.WriteString(unit.sep)
		} else if out.Len() > 0 && out.String()[out.Len()-1] != ' ' {
			out.WriteByte(' ')
		}
	}
	return strings.TrimSuffix(out.String(), " "), nil
}

var rewriteWordRE = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func bigramDivergence(before, after string) float64 {
	bigrams := func(text string) map[string]bool {
		words := rewriteWordRE.FindAllString(strings.ToLower(text), -1)
		result := map[string]bool{}
		for i := 0; i+1 < len(words); i++ {
			result[words[i]+" "+words[i+1]] = true
		}
		return result
	}
	left, right := bigrams(before), bigrams(after)
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	intersection := 0
	for key := range left {
		if right[key] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return 1 - float64(intersection)/float64(union)
}

func rewriteEndpointIsLoopback(endpoint, provider string) bool {
	if strings.EqualFold(provider, "ollama") && (endpoint == "" || strings.Contains(endpoint, "api.openai.com")) {
		endpoint = "http://127.0.0.1:11434/api/chat"
	}
	if endpoint == "" {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	setLocalizedFlagUsage(fs, name)
	return fs
}

func callRewriteProvider(provider, baseURL, apiKey, model, instruction, input string, temperature float64, allowRemote bool, reasoningEffort string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "ollama":
		if baseURL == "" || strings.Contains(baseURL, "api.openai.com") {
			baseURL = "http://127.0.0.1:11434/api/chat"
		}
		body := map[string]any{"model": model, "messages": []rewriteMessage{{Role: "user", Content: instruction + "\n\n" + input}}, "stream": false, "options": map[string]any{"temperature": temperature}}
		request, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		response, err := postJSON(baseURL, "", request, allowRemote)
		if err != nil {
			return "", err
		}
		var decoded struct {
			Message  rewriteMessage `json:"message"`
			Response string         `json:"response"`
		}
		if err := json.Unmarshal(response, &decoded); err != nil {
			return "", fmt.Errorf("decode Ollama response: %w", err)
		}
		if decoded.Message.Content != "" {
			return decoded.Message.Content, nil
		}
		if decoded.Response != "" {
			return decoded.Response, nil
		}
		return "", errors.New("Ollama response did not contain message.content")
	case "openai", "openai-compatible", "openai_compatible":
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1/chat/completions"
		}
		body, err := json.Marshal(chatRequest{Model: model, Messages: []rewriteMessage{{Role: "system", Content: instruction}, {Role: "user", Content: input}}, Temperature: temperature, ReasoningEffort: normalizedReasoningEffort(reasoningEffort)})
		if err != nil {
			return "", err
		}
		response, err := postJSON(baseURL, apiKey, body, allowRemote)
		if err != nil {
			return "", err
		}
		var decoded chatResponse
		if err := json.Unmarshal(response, &decoded); err != nil {
			return "", fmt.Errorf("decode OpenAI-compatible response: %w", err)
		}
		if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == "" {
			return "", errors.New("OpenAI-compatible response did not contain choices[0].message.content")
		}
		return decoded.Choices[0].Message.Content, nil
	default:
		return "", fmt.Errorf("unknown rewrite provider %q", provider)
	}
}

func normalizedReasoningEffort(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "off" {
		return ""
	}
	return value
}

func postJSON(endpoint, apiKey string, body []byte, allowRemote bool) ([]byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("rewrite endpoint must use http or https")
	}
	if !allowRemote && !rewriteEndpointIsLoopback(endpoint, "") {
		return nil, errors.New("remote rewrite endpoint denied; pass --allow-remote explicitly")
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{
		Timeout: 20 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("rewrite endpoint redirects are refused")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	maxBytes := core.MaxInputBytes()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("rewrite provider response exceeds %d bytes", maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("rewrite provider returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func writeRewriteOutput(inputName string, data []byte, requested string, inPlace bool, metadata map[string]any, jsonStats bool) int {
	output := requested
	mode := os.FileMode(0o644)
	if inputName != "stdin.txt" {
		if st, err := os.Stat(inputName); err == nil {
			mode = st.Mode().Perm()
		}
	}
	if inPlace {
		if inputName == "stdin.txt" {
			return cliError(errors.New("--in-place requires a file input"))
		}
		output = inputName
		if _, _, err := core.BackupFile(inputName, core.ReflinkAuto); err != nil {
			return cliError(fmt.Errorf("create backup: %w", err))
		}
	} else if output == "" && inputName != "stdin.txt" {
		ext := filepath.Ext(inputName)
		output = strings.TrimSuffix(inputName, ext) + ".rewritten" + ext
	}
	if output == "" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := core.WriteFileAtomic(output, data, mode); err != nil {
		return cliError(err)
	}
	metadata["input"] = inputName
	metadata["output"] = output
	metadata["changed"] = true
	metadata["bytes_out"] = len(data)
	if jsonStats || os.Getenv("AIWR_REWRITE_JSON") == "1" {
		writeJSON(metadata)
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", cliText(
			fmt.Sprintf("%s -> %s: rewritten (%d bytes)", inputName, output, len(data)),
			fmt.Sprintf("%s → %s：已重写（%d 字节）", inputName, output, len(data)),
		))
	}
	return 0
}
