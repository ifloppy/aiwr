package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultLayerBStrategy is the strategy shipped by the upstream service.
// Layer B is intentionally separate from the deterministic Layer A pipeline:
// it needs a configured model backend and is never invoked by CleanFile.
const DefaultLayerBStrategy = "paraphrase@0.8,mlm@0.2"

// LayerBStep is one tactic and its requested rewrite intensity.
type LayerBStep struct {
	Tactic    string
	Intensity float64
}

type layerBMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type layerBChatResponse struct {
	Choices []struct {
		Message layerBMessage `json:"message"`
	} `json:"choices"`
	Message layerBMessage `json:"message"`
}

// ParseLayerBStrategy parses the same tactic@intensity notation used by the
// upstream rewrite service.
func ParseLayerBStrategy(spec string) ([]LayerBStep, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, errors.New("strategy must be a non-empty list of tactic@intensity steps")
	}
	steps := make([]LayerBStep, 0, 2)
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return nil, errors.New("bad empty strategy step; expected tactic@intensity")
		}
		at := strings.LastIndexByte(item, '@')
		if at <= 0 || at == len(item)-1 {
			return nil, fmt.Errorf("bad strategy step %q; expected tactic@intensity", item)
		}
		tactic := strings.ToLower(strings.TrimSpace(item[:at]))
		switch tactic {
		case "paraphrase", "backtranslate", "structural", "humanize", "code", "chunk", "mlm":
		default:
			return nil, fmt.Errorf("unknown strategy tactic %q", tactic)
		}
		level, err := strconv.ParseFloat(strings.TrimSpace(item[at+1:]), 64)
		if err != nil || level <= 0 || level > 1 {
			return nil, fmt.Errorf("strategy intensity must be in (0,1], got %q", strings.TrimSpace(item[at+1:]))
		}
		steps = append(steps, LayerBStep{Tactic: tactic, Intensity: level})
	}
	return steps, nil
}

// ApplyLayerB applies a configured sequential rewrite strategy to text.
// Errors are explicit when the optional backend is not configured, so callers
// can surface a failed optional step instead of claiming that it ran.
func ApplyLayerB(text string, opts Options) (string, map[string]any, error) {
	if !utf8.ValidString(text) {
		return "", nil, errors.New("Layer B requires valid UTF-8 text")
	}
	var err error
	strategy := strings.TrimSpace(opts.Strategy)
	if strategy == "" {
		strategy = strings.TrimSpace(os.Getenv("WATERMARKS_CLEAN_STRATEGY"))
	}
	if strategy == "" {
		strategy, err = configuredLayerBStrategy()
		if err != nil {
			return "", nil, err
		}
	}
	steps, err := ParseLayerBStrategy(strategy)
	if err != nil {
		return "", nil, err
	}

	backend := strings.TrimSpace(opts.RewriteBackend)
	if backend == "" {
		backend = strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_BACKEND"))
	}
	if backend == "" {
		backend = "print-prompt"
	}
	backend = strings.ToLower(backend)
	if backend == "openai" || backend == "openai_compatible" {
		backend = "openai-compatible"
	}
	if backend != "print-prompt" && backend != "ollama" && backend != "openai-compatible" {
		return "", nil, fmt.Errorf("unknown rewrite backend %q", backend)
	}

	needsLLM := false
	hasMLM := false
	for _, step := range steps {
		if step.Tactic == "mlm" {
			hasMLM = true
		} else {
			needsLLM = true
		}
	}
	if hasMLM {
		if strings.TrimSpace(externalScriptsDir(opts)) == "" {
			return "", nil, errors.New("Layer B 'mlm' step requires the optional transformers backend; pass --upstream-scripts or set AIWR_UPSTREAM_SCRIPTS")
		}
		return applyLayerBExternal(text, strategy, opts)
	}
	if !needsLLM {
		return "", nil, errors.New("Layer B strategy contains no executable rewrite step")
	}
	if backend == "print-prompt" {
		return "", nil, errors.New("Layer B strategy needs an LLM rewrite backend (set WATERMARKS_REWRITE_BACKEND)")
	}

	model := strings.TrimSpace(opts.RewriteModel)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_MODEL"))
	}
	baseURL := strings.TrimSpace(opts.RewriteBaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_BASE_URL"))
	}
	if model == "" || baseURL == "" {
		required := "WATERMARKS_REWRITE_MODEL and WATERMARKS_REWRITE_BASE_URL"
		return "", nil, fmt.Errorf("Layer B strategy needs the rewrite backend configured (%s)", required)
	}
	apiKey := strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if backend == "openai-compatible" && apiKey == "" {
		return "", nil, errors.New("Layer B openai-compatible backend needs WATERMARKS_REWRITE_API_KEY")
	}
	allowRemote := opts.RewriteAllowRemote || layerBEnvTrue("WATERMARKS_REWRITE_ALLOW_REMOTE")
	if err := validateLayerBURL(baseURL, allowRemote); err != nil {
		return "", nil, err
	}
	endpoint, err := layerBEndpoint(baseURL, backend)
	if err != nil {
		return "", nil, err
	}
	timeoutSeconds := opts.RewriteTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	if timeoutSeconds > 30*60 {
		return "", nil, errors.New("rewrite timeout must not exceed 1800 seconds")
	}
	temperature := opts.RewriteTemperature
	if temperature < 0 || temperature > 2 {
		return "", nil, errors.New("rewrite temperature must be between 0 and 2")
	}

	current := text
	stepStats := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		inChars := len(current)
		prompt := layerBPrompt(step.Tactic, current, step.Intensity, opts.RewriteStyle)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		output, callErr := callLayerBBackend(ctx, endpoint, backend, apiKey, model, prompt, temperature)
		cancel()
		if callErr != nil {
			return "", nil, fmt.Errorf("Layer B %s step failed: %w", step.Tactic, callErr)
		}
		if step.Tactic == "humanize" {
			output = HumanizeText(output)
		}
		current = strings.TrimSpace(output)
		if current == "" {
			return "", nil, fmt.Errorf("Layer B %s step returned empty text", step.Tactic)
		}
		stepStats = append(stepStats, map[string]any{
			"tactic": step.Tactic, "intensity": step.Intensity,
			"in_chars": inChars, "out_chars": len(current),
		})
	}
	if opts.LayerAAfter {
		cleaned, _ := cleanText([]byte(current), opts)
		current = string(cleaned)
	}
	strategyNames := make([]string, 0, len(steps))
	for _, step := range steps {
		strategyNames = append(strategyNames, fmt.Sprintf("%s@%g", step.Tactic, step.Intensity))
	}
	stats := map[string]any{
		"backend": backend, "tactic": "strategy", "mode": "strategy",
		"strategy": strategyNames, "steps": stepStats,
		"input_chars": len(text), "output_chars": len(current),
		"layer_a_after": opts.LayerAAfter,
	}
	return current, stats, nil
}

func applyLayerBExternal(text, strategy string, opts Options) (string, map[string]any, error) {
	current, stats, err := runExternalRewrite(text, strategy, opts)
	if err != nil {
		return "", nil, err
	}
	if opts.LayerAAfter {
		cleaned, _ := cleanText([]byte(current), opts)
		current = string(cleaned)
	}
	if strings.TrimSpace(current) == "" {
		return "", nil, errors.New("Layer B external rewrite returned empty text")
	}
	if stats == nil {
		stats = map[string]any{}
	}
	backend := strings.TrimSpace(opts.RewriteBackend)
	if backend == "" {
		backend = strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_BACKEND"))
	}
	if backend == "" {
		backend = "external"
	} else {
		backend = "external-" + backend
	}
	stats["backend"] = backend
	stats["input_chars"] = len(text)
	stats["output_chars"] = len(current)
	stats["layer_a_after"] = opts.LayerAAfter
	return current, stats, nil
}

func configuredLayerBStrategy() (string, error) {
	configPath := strings.TrimSpace(os.Getenv("WATERMARKS_CLEAN_STRATEGY_FILE"))
	if configPath == "" {
		configPath = "config/clean_strategy.json"
	}
	raw, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultLayerBStrategy, nil
	}
	if err != nil {
		return "", fmt.Errorf("read Layer B strategy config %s: %w", configPath, err)
	}
	var config struct {
		DefaultStrategy string `json:"default_strategy"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("invalid Layer B strategy config %s: %w", configPath, err)
	}
	if strings.TrimSpace(config.DefaultStrategy) == "" {
		return "", fmt.Errorf("Layer B strategy config %s has no default_strategy", configPath)
	}
	return strings.TrimSpace(config.DefaultStrategy), nil
}

func layerBPrompt(tactic, text string, intensity float64, style string) string {
	var instruction string
	switch tactic {
	case "paraphrase":
		instruction = "Rewrite the following text with substantially different wording at the token level. Vary clause order, connectors, sentence boundaries, and sentence length. Preserve every fact, number, name, and technical identifier. Do not add or remove claims. Output only the rewritten text."
	case "backtranslate":
		instruction = "Rewrite the following text as if it were translated into another language and naturally translated back. Preserve every fact, number, name, and technical identifier. Do not add or remove claims. Output only the rewritten text."
	case "structural":
		instruction = "Rewrite the following text with a natural variation in sentence structure and paragraph flow. Preserve every fact, number, name, and technical identifier. Do not add or remove claims. Output only the rewritten text."
	case "humanize":
		instruction = "Rewrite the following text so it reads as if a human wrote it from scratch. Use uneven sentence rhythm, plain concrete wording, and simple verbs. Avoid formulaic transitions and promotional filler. Preserve every fact, number, name, and technical identifier. Do not add or remove claims. Output only the rewritten text."
	case "code":
		instruction = "Rewrite the natural-language parts of the following code, including comments, docstrings, and string literals, using different wording. Preserve behavior, public API names, syntax, and output-affecting values. Output only the rewritten code."
	case "chunk":
		instruction = "Rewrite the following text in coherent local chunks while preserving facts, meaning, ordering, and approximate length. Output only the rewritten text."
	}
	instruction += fmt.Sprintf("\nModulate the rewrite so roughly %.2f of tokens change. Preserve facts and do not add or remove claims.", intensity)
	if style != "" {
		instruction += "\nApply this writing style throughout the rewrite while keeping it subordinate to the content: " + style
	}
	return instruction + "\n\n---\n" + text
}

func layerBEndpoint(baseURL, backend string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid rewrite base URL: %w", err)
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch backend {
	case "ollama":
		if !strings.HasSuffix(path, "/api/chat") {
			if strings.HasSuffix(path, "/api") {
				path += "/chat"
			} else {
				path += "/api/chat"
			}
		}
	case "openai-compatible":
		if !strings.HasSuffix(path, "/chat/completions") {
			if strings.HasSuffix(path, "/v1") {
				path += "/chat/completions"
			} else {
				path += "/v1/chat/completions"
			}
		}
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), nil
}

func validateLayerBURL(raw string, allowRemote bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return errors.New("rewrite base URL must use http or https and include a host")
	}
	if parsed.User != nil {
		return errors.New("rewrite base URL must not include userinfo")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return nil
	}
	if !allowRemote {
		return errors.New("Layer B strategy uses a remote rewrite endpoint; set WATERMARKS_REWRITE_ALLOW_REMOTE=1")
	}
	return nil
}

func callLayerBBackend(ctx context.Context, endpoint, backend, apiKey, model, prompt string, temperature float64) (string, error) {
	var body []byte
	var err error
	switch backend {
	case "ollama":
		body, err = json.Marshal(map[string]any{
			"model": model, "stream": false,
			"messages": []layerBMessage{{Role: "user", Content: prompt}},
			"options":  map[string]any{"temperature": temperature},
		})
	case "openai-compatible":
		body, err = json.Marshal(map[string]any{
			"model":       model,
			"messages":    []layerBMessage{{Role: "user", Content: prompt}},
			"temperature": temperature,
		})
	default:
		return "", fmt.Errorf("unsupported backend %q", backend)
	}
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("rewrite endpoint redirects are refused")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	maxBytes := maxInputBytes()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(payload)) > maxBytes {
		return "", fmt.Errorf("rewrite backend response exceeds %d bytes", maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(payload))
		if len(message) > 4096 {
			message = message[:4096]
		}
		return "", fmt.Errorf("rewrite backend returned HTTP %d: %s", response.StatusCode, message)
	}
	var decoded layerBChatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("decode rewrite backend response: %w", err)
	}
	if len(decoded.Choices) > 0 && strings.TrimSpace(decoded.Choices[0].Message.Content) != "" {
		return decoded.Choices[0].Message.Content, nil
	}
	if strings.TrimSpace(decoded.Message.Content) != "" {
		return decoded.Message.Content, nil
	}
	return "", errors.New("rewrite backend response did not contain generated message content")
}

func layerBEnvTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
