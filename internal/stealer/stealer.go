// Package stealer contains the model-free core and network helpers for the
// upstream black-box watermark-stealing research workflow. It is deliberately
// separate from the cleaning pipeline: building a scorer does not recover a
// secret key and applying it is only a model caller's logit adjustment hook.
package stealer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const DryReplyHead = "This is a dry-run reply to the prompt: "

// ErrInterrupted means that a prompt download stopped after the last
// committed page. The output and resume state are left consistent.
var ErrInterrupted = errors.New("download interrupted; resume from the last complete page")

type NGramCounts struct {
	Contexts map[string]map[string]int
	Totals   map[string]int
	Unigrams map[string]int
}

// DefaultTokenize mirrors the upstream stdlib fallback tokenizer: lower-case
// word/number runs and punctuation are separate tokens.
func DefaultTokenize(text string) []string {
	tokens := []string{}
	var word []rune
	flush := func() {
		if len(word) > 0 {
			tokens = append(tokens, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			word = append(word, r)
			continue
		}
		flush()
		if !unicode.IsSpace(r) {
			tokens = append(tokens, strings.ToLower(string(r)))
		}
	}
	flush()
	return tokens
}

func ContextKey(context []string) string {
	data, _ := json.Marshal(context)
	return string(data)
}

func CountNGrams(texts []string, contextLen int) NGramCounts {
	counts := NGramCounts{
		Contexts: map[string]map[string]int{},
		Totals:   map[string]int{},
		Unigrams: map[string]int{},
	}
	if contextLen < 0 {
		return counts
	}
	for _, text := range texts {
		tokens := DefaultTokenize(text)
		for _, token := range tokens {
			counts.Unigrams[token]++
		}
		for i := contextLen; i < len(tokens); i++ {
			key := ContextKey(tokens[i-contextLen : i])
			bucket := counts.Contexts[key]
			if bucket == nil {
				bucket = map[string]int{}
				counts.Contexts[key] = bucket
			}
			bucket[tokens[i]]++
			counts.Totals[key]++
		}
	}
	return counts
}

func probability(count, total, vocabulary int, alpha float64) float64 {
	denominator := float64(total) + alpha*float64(maxInt(vocabulary, 1))
	if denominator < 1 {
		denominator = 1
	}
	return (float64(count) + alpha) / denominator
}

func round5(value float64) float64 {
	return math.Round(value*100000) / 100000
}

// BuildScorer derives s*(token|context), using a baseline context distribution
// where available and a unigram fallback otherwise.
func BuildScorer(watermarked, baseline NGramCounts, contextLen, topK int, alpha float64, minContext int) map[string]any {
	if topK < 0 {
		topK = 0
	}
	wmVocabulary := maxInt(len(watermarked.Unigrams), 1)
	baseVocabulary := maxInt(len(baseline.Unigrams), 1)
	baseUnigramTotal := 0
	for _, count := range baseline.Unigrams {
		baseUnigramTotal += count
	}
	if baseUnigramTotal == 0 {
		baseUnigramTotal = 1
	}
	type scoredToken struct {
		token string
		score float64
	}
	scorer := map[string]any{}
	keys := make([]string, 0, len(watermarked.Contexts))
	for key := range watermarked.Contexts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if watermarked.Totals[key] < minContext {
			continue
		}
		wmTotal := watermarked.Totals[key]
		baseTotal := baseline.Totals[key]
		baseBucket := baseline.Contexts[key]
		scores := make([]scoredToken, 0, len(watermarked.Contexts[key]))
		for token, count := range watermarked.Contexts[key] {
			pWM := probability(count, wmTotal, wmVocabulary, alpha)
			pBase := 0.0
			if baseBucket != nil {
				pBase = probability(baseBucket[token], baseTotal, baseVocabulary, alpha)
			} else {
				pBase = probability(baseline.Unigrams[token], baseUnigramTotal, baseVocabulary, alpha)
			}
			scores = append(scores, scoredToken{token: token, score: math.Log((pWM + 1e-6) / (pBase + 1e-6))})
		}
		sort.SliceStable(scores, func(i, j int) bool {
			if scores[i].score == scores[j].score {
				return scores[i].token < scores[j].token
			}
			return scores[i].score > scores[j].score
		})
		if topK < len(scores) {
			scores = scores[:topK]
		}
		entries := make([]any, 0, len(scores))
		for _, entry := range scores {
			entries = append(entries, map[string]any{"token": entry.token, "score": round5(entry.score)})
		}
		if len(entries) > 0 {
			scorer[key] = entries
		}
	}
	return map[string]any{
		"config": map[string]any{
			"context_len": contextLen, "topk": topK, "alpha": alpha,
			"min_context": minContext, "baseline_fallback": "unigram",
		},
		"scorer": scorer,
	}
}

func LoadScorer(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var scorer map[string]any
	if err := json.Unmarshal(data, &scorer); err != nil {
		return nil, fmt.Errorf("decode scorer: %w", err)
	}
	return scorer, nil
}

func scoreEntries(value any, token string) (float64, bool) {
	entries, ok := value.([]any)
	if !ok {
		return 0, false
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok || entry["token"] != token {
			continue
		}
		score, ok := entry["score"].(float64)
		return score, ok
	}
	return 0, false
}

func scorerContextLength(scorer map[string]any) int {
	config, _ := scorer["config"].(map[string]any)
	value, _ := config["context_len"].(float64)
	if value < 0 || value > float64(^uint(0)>>1) {
		return 8
	}
	return int(value)
}

// ScorerContextLength returns the context length stored in a scorer file, or
// the upstream-compatible default when the file has no usable configuration.
func ScorerContextLength(scorer map[string]any) int {
	return scorerContextLength(scorer)
}

func ScoreSequence(scorer map[string]any, tokens []string, contextLen int) map[string]any {
	if contextLen < 0 {
		contextLen = 0
	}
	table, _ := scorer["scorer"].(map[string]any)
	total := 0.0
	applied := 0
	for i := contextLen; i < len(tokens); i++ {
		if score, ok := scoreEntries(table[ContextKey(tokens[i-contextLen:i])], tokens[i]); ok {
			total += score
			applied++
		}
	}
	return map[string]any{"score": round5(total), "applied": applied}
}

func ApplyDelta(scorer map[string]any, context []string, logits map[string]float64, delta float64) map[string]float64 {
	table, _ := scorer["scorer"].(map[string]any)
	adjusted := map[string]float64{}
	for token, value := range logits {
		adjusted[token] = value
		if score, ok := scoreEntries(table[ContextKey(context)], token); ok {
			adjusted[token] = value - delta*score
		}
	}
	return adjusted
}

func ReadCorpus(path, field string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("not found: %s: %w", path, err)
	}
	defer file.Close()
	items := []string{}
	scanner := bufio.NewScanner(io.LimitReader(file, 256<<20))
	scannerBuffer := make([]byte, 64<<10)
	scanner.Buffer(scannerBuffer, 16<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) == nil {
				if value, ok := row[field].(string); ok && value != "" {
					items = append(items, value)
				}
				continue
			}
		}
		items = append(items, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func ReadReplies(path string) ([]string, error) {
	return ReadCorpus(path, "reply")
}

func WriteReplies(path string, rows []map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return file.Sync()
}

func DryReply(prompt string) string {
	if len([]rune(prompt)) > 200 {
		prompt = string([]rune(prompt)[:200])
	}
	return DryReplyHead + prompt
}

func loopbackURL(raw string) (bool, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false, errors.New("stealer endpoint must use http or https")
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "127.0.0.1" || host == "localhost" || host == "::1", nil
}

func OpenAIReply(prompt, baseURL, apiKey, model string, maxNewTokens int) (string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	body, err := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": maxNewTokens, "temperature": 1.0,
	})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "aiwr/stealer")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("stealer endpoint redirects are refused")
	}}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("model endpoint returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", fmt.Errorf("decode model response: %w", err)
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == "" {
		return "", errors.New("model response did not contain choices[0].message.content")
	}
	return decoded.Choices[0].Message.Content, nil
}

type QueryOptions struct {
	Prompts      string
	Out          string
	Backend      string
	BaseURL      string
	Model        string
	APIKey       string
	MaxNewTokens int
	Concurrency  int
	AllowRemote  bool
	Progress     io.Writer
}

func Query(opts QueryOptions) error {
	prompts, err := ReadCorpus(opts.Prompts, "text")
	if err != nil {
		return err
	}
	if opts.Backend == "" {
		opts.Backend = "dry-run"
	}
	if opts.Concurrency < 1 {
		return errors.New("concurrency must be at least 1")
	}
	if opts.MaxNewTokens < 1 {
		return errors.New("max-new-tokens must be at least 1")
	}
	if opts.Backend != "dry-run" && opts.Backend != "openai-compatible" {
		return fmt.Errorf("unknown stealer backend %q", opts.Backend)
	}
	if opts.Backend == "openai-compatible" {
		if opts.BaseURL == "" {
			return errors.New("base URL is required for openai-compatible backend")
		}
		local, urlErr := loopbackURL(opts.BaseURL)
		if urlErr != nil {
			return urlErr
		}
		if !local && !opts.AllowRemote {
			return errors.New("refusing to send content to a non-loopback endpoint; set --allow-remote or WATERMARKS_STEAL_ALLOW_REMOTE=1")
		}
		if opts.APIKey == "" {
			return errors.New("missing API key; set WATERMARKS_STEAL_API_KEY")
		}
		if opts.Model == "" {
			return errors.New("missing model; set WATERMARKS_STEAL_MODEL or --model")
		}
	}
	if opts.Progress == nil {
		opts.Progress = io.Discard
	}
	if _, err := fmt.Fprintf(opts.Progress, "querying %d prompts -> %s (backend=%s)\n", len(prompts), opts.Out, opts.Backend); err != nil {
		return err
	}
	replies := make([]string, len(prompts))
	worker := func(prompt string) (string, error) {
		if opts.Backend == "dry-run" {
			return DryReply(prompt), nil
		}
		return OpenAIReply(prompt, opts.BaseURL, opts.APIKey, opts.Model, opts.MaxNewTokens)
	}
	if opts.Concurrency == 1 {
		for i, prompt := range prompts {
			replies[i], err = worker(prompt)
			if err != nil {
				return err
			}
		}
	} else {
		jobs := make(chan int)
		var wait sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex
		workers := opts.Concurrency
		if workers > len(prompts) {
			workers = len(prompts)
		}
		for i := 0; i < workers; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for index := range jobs {
					answer, callErr := worker(prompts[index])
					if callErr != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = callErr
						}
						errMu.Unlock()
						continue
					}
					replies[index] = answer
				}
			}()
		}
		for index := range prompts {
			jobs <- index
		}
		close(jobs)
		wait.Wait()
		if firstErr != nil {
			return firstErr
		}
	}
	for i, prompt := range prompts {
		if err := WriteReplies(opts.Out, []map[string]string{{"prompt": prompt, "reply": replies[i]}}); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(opts.Progress, "done: %s\n", opts.Out)
	return nil
}

type DownloadOptions struct {
	Dataset   string
	Config    string
	Split     string
	Count     int
	Field     string
	Out       string
	BaseURL   string
	Delay     time.Duration
	MinChars  int
	MaxChars  int
	Offset    int
	StartOver bool
	Context   context.Context
	Progress  io.Writer
}

type downloadState struct {
	NextOffset int `json:"next_offset"`
	Written    int `json:"written"`
}

func readDownloadState(path string) downloadState {
	data, err := os.ReadFile(path)
	if err != nil {
		return downloadState{}
	}
	var state downloadState
	if json.Unmarshal(data, &state) != nil || state.NextOffset < 0 || state.Written < 0 {
		return downloadState{}
	}
	return state
}

func saveDownloadState(path string, state downloadState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := path + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func fetchRows(base, dataset, config, split string, offset, length int, token string) (map[string]any, float64, error) {
	return fetchRowsContext(context.Background(), base, dataset, config, split, offset, length, token)
}

func fetchRowsContext(ctx context.Context, base, dataset, config, split string, offset, length int, token string) (map[string]any, float64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, 0, errors.New("refusing non-http(s) datasets-server URL")
	}
	query := parsed.Query()
	query.Set("dataset", dataset)
	query.Set("config", config)
	query.Set("split", split)
	query.Set("offset", strconv.Itoa(offset))
	query.Set("length", strconv.Itoa(length))
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/rows"
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("User-Agent", "aiwr/stealer")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("datasets-server redirects are refused")
	}}).Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	retryAfter := 0.0
	if raw := response.Header.Get("Retry-After"); raw != "" {
		if value, parseErr := strconv.ParseFloat(strings.TrimSpace(raw), 64); parseErr == nil {
			retryAfter = math.Max(1, math.Min(value, 120))
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, retryAfter, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, retryAfter, &httpStatusError{code: response.StatusCode, body: string(data)}
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, retryAfter, err
	}
	return payload, retryAfter, nil
}

type httpStatusError struct {
	code int
	body string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.code, strings.TrimSpace(e.body))
}

func fetchRowsRetrying(opts DownloadOptions, offset, length int, token string) (map[string]any, error) {
	return fetchRowsRetryingContext(context.Background(), opts, offset, length, token)
}

func fetchRowsRetryingContext(ctx context.Context, opts DownloadOptions, offset, length int, token string) (map[string]any, error) {
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, ErrInterrupted
		}
		payload, retryAfter, err := fetchRowsContext(ctx, opts.BaseURL, opts.Dataset, opts.Config, opts.Split, offset, length, token)
		if err == nil {
			return payload, nil
		}
		if ctx.Err() != nil {
			return nil, ErrInterrupted
		}
		last = err
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && (statusErr.code == 400 || statusErr.code == 401 || statusErr.code == 403 || statusErr.code == 404) {
			break
		}
		if attempt == 7 {
			break
		}
		delay := retryAfter
		if delay <= 0 {
			delay = math.Min(90, 2*math.Pow(2, float64(attempt)))
		}
		if opts.Progress != nil {
			_, _ = fmt.Fprintf(opts.Progress, "retrying offset %d in %.1fs: %v\n", offset, delay, err)
		}
		timer := time.NewTimer(time.Duration(delay * float64(time.Second)))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ErrInterrupted
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("could not fetch rows at offset %d: %w", offset, last)
}

func DownloadPrompts(opts DownloadOptions) (int, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Dataset == "" {
		opts.Dataset = "allenai/c4"
	}
	if opts.Config == "" {
		opts.Config = "realnewslike"
	}
	if opts.Split == "" {
		opts.Split = "train"
	}
	if opts.Count < 1 {
		return 0, errors.New("count must be at least 1")
	}
	if opts.Field == "" {
		opts.Field = "text"
	}
	if opts.BaseURL == "" {
		opts.BaseURL = "https://datasets-server.huggingface.co"
	}
	if opts.Delay < 0 {
		return 0, errors.New("delay must be non-negative")
	}
	if opts.MinChars < 0 || opts.MaxChars < 0 {
		return 0, errors.New("character limits must be non-negative")
	}
	if opts.Progress == nil {
		opts.Progress = io.Discard
	}
	outDir := opts.Out
	if outDir == "" {
		outDir = filepath.Join("stealer", "prompts")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	promptsPath := filepath.Join(outDir, "prompts.jsonl")
	statePath := filepath.Join(outDir, ".download-state.json")
	if opts.StartOver {
		if err := removeIfExists(promptsPath); err != nil {
			return 0, err
		}
		if err := removeIfExists(statePath); err != nil {
			return 0, err
		}
	}
	state := readDownloadState(statePath)
	if opts.Offset != 0 {
		state.NextOffset = opts.Offset
	}
	if state.NextOffset < 0 || state.Written < 0 || state.Written > opts.Count {
		state = downloadState{}
	}
	token := os.Getenv("HF_TOKEN")
	probe, err := fetchRowsRetryingContext(ctx, opts, state.NextOffset, 1, token)
	if err != nil {
		return 0, err
	}
	pageSize := 100
	if raw, ok := probe["num_rows_per_page"].(float64); ok && raw >= 1 {
		pageSize = int(raw)
		if pageSize > 100 {
			pageSize = 100
		}
	}
	marker, err := os.OpenFile(promptsPath, os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	if err := marker.Close(); err != nil {
		return 0, err
	}
	_, _ = fmt.Fprintf(opts.Progress, "downloading %s:%s:%s (%d prompts) -> %s\n", opts.Dataset, opts.Config, opts.Split, opts.Count, promptsPath)
	file, err := os.OpenFile(promptsPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	committedBytes, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	committedOffset, committedWritten := state.NextOffset, state.Written
	rollback := func() {
		if truncateErr := file.Truncate(committedBytes); truncateErr == nil {
			_, _ = file.Seek(0, io.SeekEnd)
		}
		state.NextOffset, state.Written = committedOffset, committedWritten
		_ = saveDownloadState(statePath, state)
	}
	for state.Written < opts.Count {
		if err := ctx.Err(); err != nil {
			rollback()
			return state.Written, ErrInterrupted
		}
		rows, fetchErr := fetchRowsRetryingContext(ctx, opts, state.NextOffset, pageSize, token)
		if fetchErr != nil {
			if errors.Is(fetchErr, ErrInterrupted) {
				rollback()
			}
			return state.Written, fetchErr
		}
		rawRows, _ := rows["rows"].([]any)
		if len(rawRows) == 0 {
			return state.Written, errors.New("dataset exhausted before requested count")
		}
		batch := make([]byte, 0, len(rawRows)*64)
		for _, raw := range rawRows {
			if state.Written >= opts.Count {
				break
			}
			row, _ := raw.(map[string]any)
			values, _ := row["row"].(map[string]any)
			text, _ := values[opts.Field].(string)
			if text == "" || opts.MinChars > 0 && len([]rune(text)) < opts.MinChars {
				continue
			}
			if opts.MaxChars > 0 {
				runes := []rune(text)
				if len(runes) > opts.MaxChars {
					text = string(runes[:opts.MaxChars])
				}
			}
			line, marshalErr := json.Marshal(map[string]string{"text": text})
			if marshalErr != nil {
				return state.Written, marshalErr
			}
			batch = append(batch, line...)
			batch = append(batch, '\n')
			state.Written++
		}
		if err := ctx.Err(); err != nil {
			rollback()
			return committedWritten, ErrInterrupted
		}
		writtenBytes, writeErr := file.Write(batch)
		if writeErr != nil {
			rollback()
			return committedWritten, writeErr
		}
		if writtenBytes != len(batch) {
			rollback()
			return committedWritten, io.ErrShortWrite
		}
		if err := file.Sync(); err != nil {
			rollback()
			return committedWritten, err
		}
		if err := ctx.Err(); err != nil {
			rollback()
			return committedWritten, ErrInterrupted
		}
		state.NextOffset += len(rawRows)
		if err := saveDownloadState(statePath, state); err != nil {
			rollback()
			return committedWritten, err
		}
		committedBytes += int64(len(batch))
		committedOffset, committedWritten = state.NextOffset, state.Written
		if state.Written >= opts.Count {
			break
		}
		if opts.Delay > 0 {
			timer := time.NewTimer(opts.Delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return state.Written, ErrInterrupted
			case <-timer.C:
			}
		}
	}
	_, _ = fmt.Fprintf(opts.Progress, "done: %d prompts in %s\n", state.Written, promptsPath)
	return state.Written, nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
