package core

import (
	"context"
	"encoding/base64"
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
)

const (
	defaultSynthIDTimeout = 60 * time.Second
	maxSynthIDResponse    = int64(4 << 20)
)

// detectSynthID calls the optional reverse-SynthID HTTP sidecar. The sidecar
// is deliberately external: its model and license are not part of the MIT
// Go core. A configured but unavailable sidecar is reported as unavailable,
// never as a negative watermark verdict.
func detectSynthID(data []byte) map[string]any {
	baseURL := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_SCORER_URL"))
	if baseURL == "" {
		if dir := strings.TrimSpace(os.Getenv("REVERSE_SYNTHID_DIR")); dir != "" {
			return map[string]any{
				"detector":  "synthid",
				"available": false,
				"error":     "REVERSE_SYNTHID_DIR is set, but the Go core only supports the HTTP scorer sidecar",
			}
		}
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return map[string]any{
			"detector":  "synthid",
			"available": false,
			"error":     "SynthID scorer URL must use http or https and include a host",
		}
	}
	if parsed.User != nil {
		return map[string]any{
			"detector":  "synthid",
			"available": false,
			"error":     "SynthID scorer URL must not include userinfo",
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/score"
	parsed.RawPath = ""
	endpoint := parsed.String()
	body, err := json.Marshal(map[string]string{
		"file": base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return map[string]any{"detector": "synthid", "available": false, "error": err.Error()}
	}
	timeout := defaultSynthIDTimeout
	if raw := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_SCORER_TIMEOUT")); raw != "" {
		if seconds, parseErr := strconv.ParseFloat(raw, 64); parseErr == nil && seconds > 0 {
			timeout = time.Duration(seconds * float64(time.Second))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return map[string]any{"detector": "synthid", "available": false, "error": err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_SCORER_API_KEY")); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("SynthID scorer redirects are refused")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return map[string]any{
			"detector":  "synthid",
			"available": false,
			"error":     fmt.Sprintf("SynthID scorer sidecar unreachable: %v", err),
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxSynthIDResponse+1))
	if err != nil {
		return map[string]any{"detector": "synthid", "available": false, "error": err.Error()}
	}
	if int64(len(payload)) > maxSynthIDResponse {
		return map[string]any{"detector": "synthid", "available": false, "error": "SynthID scorer response exceeds 4 MiB"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(payload))
		if len(message) > 2000 {
			message = message[:2000]
		}
		return map[string]any{
			"detector":  "synthid",
			"available": false,
			"error":     fmt.Sprintf("SynthID scorer returned HTTP %d: %s", response.StatusCode, message),
		}
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return map[string]any{
			"detector":  "synthid",
			"available": false,
			"error":     fmt.Sprintf("bad SynthID scorer JSON: %v", err),
		}
	}
	if result == nil {
		return map[string]any{"detector": "synthid", "available": false, "error": "bad SynthID scorer response"}
	}
	result["detector"] = "synthid"
	if _, ok := result["available"]; !ok {
		result["available"] = true
	}
	return result
}

func detectSynthIDForOptions(data []byte, opts Options) map[string]any {
	if strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_SCORER_URL")) == "" {
		if local := runSynthIDExternal(data, opts); local != nil {
			return local
		}
	}
	return detectSynthID(data)
}

func synthidIsWatermarked(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	available, _ := entry["available"].(bool)
	if !available {
		return false
	}
	if marked, _ := entry["is_watermarked"].(bool); marked {
		return true
	}
	switch confidence := entry["confidence"].(type) {
	case float64:
		return confidence >= 0.5
	case float32:
		return confidence >= 0.5
	case int:
		return confidence >= 1
	case int64:
		return confidence >= 1
	}
	return false
}
