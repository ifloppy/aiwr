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
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultTextWatermarkTimeout = 120 * time.Second
	maxTextWatermarkTimeout     = 600 * time.Second
	maxTextWatermarkResponse    = int64(16 << 20)
)

// textWatermarkRequest is separate from apiRequest because /watermark accepts
// either a literal text prompt or a base64-encoded text file, not a file-only
// inspection payload.
type textWatermarkRequest struct {
	Text    *string         `json:"text"`
	File    string          `json:"file"`
	Name    string          `json:"name"`
	Keys    json.RawMessage `json:"keys"`
	Options json.RawMessage `json:"options"`

	textPresent bool
	filePresent bool
}

func (r *textWatermarkRequest) UnmarshalJSON(data []byte) error {
	type plain textWatermarkRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = textWatermarkRequest(decoded)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, r.textPresent = fields["text"]
	_, r.filePresent = fields["file"]
	return nil
}

var textWatermarkOptionNames = map[string]bool{
	"scheme": true, "seed": true, "max_new_tokens": true, "min_length": true,
	"temperature": true, "top_p": true, "model": true, "device": true,
	"config": true, "offline": true,
}

func parseTextWatermarkOptions(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return map[string]any{}, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, errors.New("'options' must be a dictionary")
	}
	unknown := make([]string, 0)
	for key := range values {
		if !textWatermarkOptionNames[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unsupported option(s): %s", strings.Join(unknown, ", "))
	}

	parsed := make(map[string]any, len(values))
	decodeAny := func(key string) (any, error) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(values[key]))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	readString := func(key string) error {
		value, err := decodeAny(key)
		if err != nil {
			return fmt.Errorf("'%s' must be a non-empty string", key)
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("'%s' must be a non-empty string", key)
		}
		parsed[key] = strings.TrimSpace(text)
		return nil
	}
	for _, key := range []string{"scheme", "model", "device", "config"} {
		if _, ok := values[key]; ok {
			if err := readString(key); err != nil {
				return nil, err
			}
		}
	}

	if _, ok := values["seed"]; ok {
		value, err := decodeAny("seed")
		if err != nil {
			return nil, errors.New("'seed' must be an integer")
		}
		number, ok := value.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			return nil, errors.New("'seed' must be an integer")
		}
		seed, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil {
			return nil, errors.New("'seed' must be an integer")
		}
		parsed["seed"] = seed
	}

	readBoundedInt := func(key string, min, max int64) error {
		if _, ok := values[key]; !ok {
			return nil
		}
		value, err := decodeAny(key)
		if err != nil {
			return fmt.Errorf("'%s' must be an integer", key)
		}
		var number int64
		switch value := value.(type) {
		case json.Number:
			if strings.ContainsAny(value.String(), ".eE") {
				return fmt.Errorf("'%s' must be an integer", key)
			}
			number, err = strconv.ParseInt(value.String(), 10, 64)
		case string:
			number, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		default:
			err = errors.New("not an integer")
		}
		if err != nil || number < min || number > max {
			return fmt.Errorf("'%s' must be between %d and %d", key, min, max)
		}
		parsed[key] = number
		return nil
	}
	if err := readBoundedInt("max_new_tokens", 1, 8192); err != nil {
		return nil, err
	}
	if err := readBoundedInt("min_length", 0, 8192); err != nil {
		return nil, err
	}

	readPositiveFloat := func(key string, maximum float64, bounded bool) error {
		if _, ok := values[key]; !ok {
			return nil
		}
		value, err := decodeAny(key)
		if err != nil {
			return fmt.Errorf("'%s' must be a number", key)
		}
		var rawNumber string
		switch value := value.(type) {
		case json.Number:
			rawNumber = value.String()
		case string:
			rawNumber = strings.TrimSpace(value)
		default:
			return fmt.Errorf("'%s' must be a number", key)
		}
		number, parseErr := strconv.ParseFloat(rawNumber, 64)
		if parseErr != nil || number <= 0 || (bounded && number > maximum) {
			if bounded {
				return fmt.Errorf("'%s' must be between 0 and %g", key, maximum)
			}
			return fmt.Errorf("'%s' must be greater than 0", key)
		}
		parsed[key] = number
		return nil
	}
	if err := readPositiveFloat("temperature", 0, false); err != nil {
		return nil, err
	}
	if err := readPositiveFloat("top_p", 1, true); err != nil {
		return nil, err
	}
	if rawValue, ok := values["offline"]; ok {
		var value bool
		trimmed := strings.TrimSpace(string(rawValue))
		if err := json.Unmarshal(rawValue, &value); err != nil || (trimmed != "true" && trimmed != "false") {
			return nil, errors.New("'offline' must be a boolean")
		}
		parsed["offline"] = value
	}
	return parsed, nil
}

func normalizeTextWatermarkKeys(raw json.RawMessage) ([]int64, bool, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, false, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, fmt.Errorf("unsupported keys format: %v", err)
	}
	parseOne := func(value any) (int64, error) {
		if number, ok := value.(json.Number); ok && !strings.ContainsAny(number.String(), ".eE") {
			parsed, err := strconv.ParseInt(number.String(), 10, 64)
			if err == nil {
				return parsed, nil
			}
		}
		if text, ok := value.(string); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if err == nil {
				return parsed, nil
			}
		}
		return 0, fmt.Errorf("%v is not an integer key", value)
	}
	switch value := value.(type) {
	case []any:
		keys := make([]int64, 0, len(value))
		for _, item := range value {
			if item == nil {
				return nil, false, errors.New("all keys in list must be integers")
			}
			key, err := parseOne(item)
			if err != nil {
				return nil, false, fmt.Errorf("all keys in list must be integers: %w", err)
			}
			keys = append(keys, key)
		}
		return keys, true, nil
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return nil, false, nil
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			var list []any
			listDecoder := json.NewDecoder(strings.NewReader(text))
			listDecoder.UseNumber()
			if err := listDecoder.Decode(&list); err != nil {
				return nil, false, fmt.Errorf("invalid JSON array for keys: %v", err)
			}
			keys := make([]int64, 0, len(list))
			for _, item := range list {
				key, err := parseOne(item)
				if err != nil {
					return nil, false, fmt.Errorf("all keys in list must be integers: %w", err)
				}
				keys = append(keys, key)
			}
			return keys, true, nil
		}
		parts := strings.Split(text, ",")
		keys := make([]int64, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			key, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return nil, false, fmt.Errorf("comma-separated keys must all be integers: %v", err)
			}
			keys = append(keys, key)
		}
		return keys, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported keys format: %T", value)
	}
}

func textWatermarkTimeout(remaining time.Duration) time.Duration {
	if remaining <= 0 {
		remaining = defaultTextWatermarkTimeout
		if raw := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_TEXT_TIMEOUT")); raw != "" {
			if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
				remaining = time.Duration(seconds * float64(time.Second))
			}
		}
	}
	if remaining < time.Second {
		remaining = time.Second
	}
	if remaining > maxTextWatermarkTimeout {
		remaining = maxTextWatermarkTimeout
	}
	return remaining
}

func extractTextWatermarkInput(req textWatermarkRequest) (string, string, error) {
	if req.textPresent || req.Text != nil {
		if req.Text == nil {
			return "", "", errors.New("'text' must be a non-empty string")
		}
		if strings.TrimSpace(*req.Text) == "" {
			return "", "", errors.New("'text' must be a non-empty string")
		}
		if int64(len([]byte(*req.Text))) > maxInputBytes() {
			return "", "", fmt.Errorf("'text' exceeds input size cap (%d bytes)", maxInputBytes())
		}
		return *req.Text, req.Name, nil
	}
	if !req.filePresent && strings.TrimSpace(req.File) == "" {
		return "", "", errors.New("request must include 'text' or base64 'file'")
	}
	data, err := decodeAPIData(apiRequest{File: req.File})
	if err != nil {
		return "", "", err
	}
	if why := isBinary(data); why != "" {
		return "", "", errors.New("refusing to treat binary content as text for watermarking")
	}
	if !utf8.Valid(data) {
		return "", "", errors.New("watermark file must be valid UTF-8")
	}
	name := req.Name
	if name == "" {
		name = "input"
	} else {
		name = safeAPIName(name)
	}
	return string(data), name, nil
}

func watermarkBackendConfigured(opts Options) bool {
	return strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_TEXT_URL")) != "" ||
		strings.TrimSpace(os.Getenv("MARKLLM_DIR")) != "" ||
		strings.TrimSpace(opts.MarkLLMDir) != ""
}

func watermarkErrorStatus(result map[string]any) int {
	code, _ := result["error_code"].(string)
	switch code {
	case "unconfigured":
		return http.StatusServiceUnavailable
	case "auth", "backend_error", "unreachable", "timeout", "ssrf":
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func generateTextWatermark(text string, keys []int64, hasKeys bool, options map[string]any, timeout time.Duration, opts Options) map[string]any {
	if strings.TrimSpace(text) == "" {
		return map[string]any{"ok": false, "kind": "text", "error": "'text' must be a non-empty string", "error_code": "client_error"}
	}
	if int64(len([]byte(text))) > maxInputBytes() {
		return map[string]any{"ok": false, "kind": "text", "error": fmt.Sprintf("'text' exceeds input size cap (%d bytes)", maxInputBytes()), "error_code": "client_error"}
	}
	if urlValue := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_TEXT_URL")); urlValue != "" {
		return watermarkViaSidecar(text, urlValue, keys, hasKeys, options, textWatermarkTimeout(timeout))
	}
	if strings.TrimSpace(os.Getenv("MARKLLM_DIR")) != "" || strings.TrimSpace(opts.MarkLLMDir) != "" {
		localTimeout := timeout
		if localTimeout <= 0 {
			localTimeout = textWatermarkTimeout(0)
		}
		return watermarkViaLocalAdapter(text, keys, hasKeys, options, localTimeout, opts)
	}
	return map[string]any{
		"ok": false, "kind": "text",
		"error":      "no text watermark generator configured (set WATERMARKS_SYNTHID_TEXT_URL for the sidecar or MARKLLM_DIR for local execution)",
		"error_code": "unconfigured",
	}
}

func watermarkViaSidecar(text, baseURL string, keys []int64, hasKeys bool, options map[string]any, timeout time.Duration) map[string]any {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return map[string]any{"ok": false, "kind": "text", "error": fmt.Sprintf("refusing non-http(s) watermark endpoint: %s", baseURL), "error_code": "backend_error"}
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/watermark"
	body := map[string]any{"text": text, "options": options}
	if hasKeys {
		body["keys"] = keys
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return map[string]any{"ok": false, "kind": "text", "error": err.Error(), "error_code": "backend_error"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return map[string]any{"ok": false, "kind": "text", "error": err.Error(), "error_code": "backend_error"}
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	if key := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_TEXT_API_KEY")); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		code := "unreachable"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "timeout"
		}
		return map[string]any{"ok": false, "kind": "text", "error": "SynthID text sidecar unreachable: " + err.Error(), "error_code": code}
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return map[string]any{"ok": false, "kind": "text", "error": "SynthID text sidecar redirect refused (SSRF protection)", "error_code": "ssrf"}
	}
	if response.StatusCode == http.StatusUnauthorized {
		return map[string]any{"ok": false, "kind": "text", "error": "SynthID text sidecar authentication error (401)", "error_code": "auth"}
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return map[string]any{"ok": false, "kind": "text", "error": fmt.Sprintf("SynthID text sidecar client error (%d)", response.StatusCode), "error_code": "client_error"}
	}
	if response.StatusCode >= 500 {
		return map[string]any{"ok": false, "kind": "text", "error": fmt.Sprintf("SynthID text sidecar HTTP error (%d)", response.StatusCode), "error_code": "backend_error"}
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, maxTextWatermarkResponse+1))
	if readErr != nil || int64(len(payload)) > maxTextWatermarkResponse {
		return map[string]any{"ok": false, "kind": "text", "error": "bad watermark sidecar response", "error_code": "backend_error"}
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil || result == nil {
		return map[string]any{"ok": false, "kind": "text", "error": "bad watermark sidecar response", "error_code": "backend_error"}
	}
	return result
}

func watermarkViaLocalAdapter(text string, keys []int64, hasKeys bool, options map[string]any, timeout time.Duration, opts Options) map[string]any {
	_, scriptsDir, err := externalScript("text_watermark.py", opts)
	if err != nil {
		return map[string]any{"ok": false, "kind": "text", "error": err.Error(), "error_code": "backend_error"}
	}
	python := externalPython(scriptsDir)
	if python == "" {
		return map[string]any{"ok": false, "kind": "text", "error": "python3 is not available for the external adapter", "error_code": "backend_error"}
	}
	request := map[string]any{"text": text, "options": options, "timeout": timeout.Seconds()}
	if hasKeys {
		request["keys"] = keys
	}
	body, err := json.Marshal(request)
	if err != nil {
		return map[string]any{"ok": false, "kind": "text", "error": err.Error(), "error_code": "backend_error"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	code := "import json,sys; from text_watermark import generate_watermark_text; r=json.load(sys.stdin); o=generate_watermark_text(r['text'], keys=r.get('keys'), options=r.get('options') or {}, timeout=r.get('timeout')); json.dump(o,sys.stdout,ensure_ascii=False)"
	command := exec.CommandContext(ctx, python, "-c", code)
	command.Dir = scriptsDir
	command.Stdin = bytes.NewReader(body)
	var stdout, stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if dir := strings.TrimSpace(opts.MarkLLMDir); dir != "" {
		command.Env = environmentWithOverrides(map[string]string{"MARKLLM_DIR": dir})
	}
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return map[string]any{"ok": false, "kind": "text", "error": "watermark generation timed out", "error_code": "timeout"}
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return map[string]any{"ok": false, "kind": "text", "error": "MarkLLM generation failed: " + truncate(message, 2000), "error_code": "backend_error"}
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result == nil {
		return map[string]any{"ok": false, "kind": "text", "error": "bad local watermark adapter response", "error_code": "backend_error"}
	}
	return result
}

func (s *apiServer) watermark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	var req textWatermarkRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	text, _, err := extractTextWatermarkInput(req)
	if err != nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	keys, hasKeys, err := normalizeTextWatermarkKeys(req.Keys)
	if err != nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	options, err := parseTextWatermarkOptions(req.Options)
	if err != nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if !watermarkBackendConfigured(DefaultOptions()) {
		writeAPIJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "kind": "text",
			"error":      "no text watermark generator configured (set WATERMARKS_SYNTHID_TEXT_URL for the sidecar or MARKLLM_DIR for local execution)",
			"error_code": "unconfigured",
		})
		return
	}
	result := generateTextWatermark(text, keys, hasKeys, options, 0, DefaultOptions())
	status := http.StatusOK
	if ok, _ := result["ok"].(bool); !ok {
		status = watermarkErrorStatus(result)
	}
	writeAPIJSON(w, status, result)
}

func (s *apiServer) watermarkBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	var body struct {
		Files json.RawMessage `json:"files"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	if len(body.Files) == 0 || strings.TrimSpace(string(body.Files)) == "null" {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "missing array field 'files'"})
		return
	}
	var files []json.RawMessage
	if err := json.Unmarshal(body.Files, &files); err != nil || files == nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "missing array field 'files'"})
		return
	}
	if len(files) == 0 {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "'files' must not be empty"})
		return
	}
	if len(files) > s.maxBatch {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: fmt.Sprintf("'files' exceeds the %d-file batch limit", s.maxBatch)})
		return
	}
	if !watermarkBackendConfigured(DefaultOptions()) {
		writeAPIJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":         false,
			"error":      "no text watermark generator configured (set WATERMARKS_SYNTHID_TEXT_URL for the sidecar or MARKLLM_DIR for local execution)",
			"error_code": "unconfigured",
		})
		return
	}
	deadline := time.Now().Add(textWatermarkTimeout(0))
	results := make([]map[string]any, 0, len(files))
	timedOut := false
	for _, raw := range files {
		if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
			results = append(results, map[string]any{
				"name": "", "ok": false, "error": "each entry in 'files' must be an object",
			})
			continue
		}
		var req textWatermarkRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			results = append(results, map[string]any{
				"name": "", "ok": false, "error": "each entry in 'files' must be an object",
			})
			continue
		}
		name := req.Name
		if timedOut {
			results = append(results, map[string]any{"name": name, "ok": false, "error": "batch deadline exceeded"})
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			timedOut = true
			results = append(results, map[string]any{"name": name, "ok": false, "error": "batch deadline exceeded"})
			continue
		}
		text, extractedName, inputErr := extractTextWatermarkInput(req)
		if name == "" {
			name = extractedName
		}
		if inputErr != nil {
			results = append(results, map[string]any{"name": name, "ok": false, "error": inputErr.Error()})
			continue
		}
		keys, hasKeys, keyErr := normalizeTextWatermarkKeys(req.Keys)
		if keyErr != nil {
			results = append(results, map[string]any{"name": name, "ok": false, "error": keyErr.Error()})
			continue
		}
		options, optionErr := parseTextWatermarkOptions(req.Options)
		if optionErr != nil {
			results = append(results, map[string]any{"name": name, "ok": false, "error": optionErr.Error()})
			continue
		}
		result := generateTextWatermark(text, keys, hasKeys, options, remaining, DefaultOptions())
		if ok, _ := result["ok"].(bool); ok {
			result["name"] = name
			results = append(results, result)
			continue
		}
		if code, _ := result["error_code"].(string); code == "timeout" {
			timedOut = true
		}
		errorText, _ := result["error"].(string)
		if errorText == "" {
			errorText = "watermark generation failed"
		}
		results = append(results, map[string]any{"name": name, "ok": false, "error": errorText})
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}
