package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postJSON(t *testing.T, handler http.Handler, path string, payload any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	return response.Code, decoded
}

func TestTextWatermarkHTTPUnconfiguredAndValidation(t *testing.T) {
	t.Setenv("WATERMARKS_SYNTHID_TEXT_URL", "")
	t.Setenv("MARKLLM_DIR", "")
	handler := NewHTTPHandler("")

	status, body := postJSON(t, handler, "/watermark", map[string]any{"text": "hello"})
	if status != http.StatusServiceUnavailable || body["ok"] != false {
		t.Fatalf("unconfigured watermark response = %d %#v", status, body)
	}
	if !strings.Contains(body["error"].(string), "WATERMARKS_SYNTHID_TEXT_URL") {
		t.Fatalf("unconfigured error = %#v", body["error"])
	}

	status, body = postJSON(t, handler, "/watermark", map[string]any{
		"text": "hello", "options": map[string]any{"unknown": true},
	})
	if status != http.StatusBadRequest || body["ok"] != false || !strings.Contains(body["error"].(string), "unsupported option") {
		t.Fatalf("invalid watermark options response = %d %#v", status, body)
	}

	status, body = postJSON(t, handler, "/watermark", map[string]any{})
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "must include 'text' or base64 'file'") {
		t.Fatalf("missing watermark input response = %d %#v", status, body)
	}
}

func TestTextWatermarkHTTPSidecarAndBatch(t *testing.T) {
	t.Setenv("MARKLLM_DIR", "")
	t.Setenv("WATERMARKS_SYNTHID_TEXT_API_KEY", "sidecar-secret")
	var requests []map[string]any
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/watermark" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sidecar-secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests = append(requests, request)
		text, _ := request["text"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "kind": "text", "watermarked_text": "WM: " + text,
			"report": map[string]any{"scheme_used": "synthid", "keys_used": request["keys"]},
		})
	}))
	defer sidecar.Close()
	t.Setenv("WATERMARKS_SYNTHID_TEXT_URL", sidecar.URL)
	handler := NewHTTPHandler("")

	status, body := postJSON(t, handler, "/watermark", map[string]any{
		"text": "hello", "keys": []int{118, 504}, "options": map[string]any{"seed": 42},
	})
	if status != http.StatusOK || body["ok"] != true || body["watermarked_text"] != "WM: hello" {
		t.Fatalf("sidecar response = %d %#v", status, body)
	}
	if len(requests) != 1 || requests[0]["text"] != "hello" {
		t.Fatalf("sidecar request = %#v", requests)
	}
	if _, ok := requests[0]["keys"].([]any); !ok {
		t.Fatalf("sidecar keys were not forwarded: %#v", requests[0])
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("from file"))
	status, body = postJSON(t, handler, "/watermark/batch", map[string]any{
		"files": []map[string]any{
			{"name": "first.txt", "text": "one"},
			{"name": "bad.txt"},
			{"name": "third.txt", "file": encoded},
		},
	})
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("batch response = %d %#v", status, body)
	}
	results, ok := body["results"].([]any)
	if !ok || len(results) != 3 {
		t.Fatalf("batch results = %#v", body["results"])
	}
	if results[0].(map[string]any)["ok"] != true || results[1].(map[string]any)["ok"] != false || results[2].(map[string]any)["ok"] != true {
		t.Fatalf("batch result statuses = %#v", results)
	}
}

func TestTextWatermarkSidecarFailureAndOpenAPI(t *testing.T) {
	t.Setenv("MARKLLM_DIR", "")
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error":"unauthorized"}`)
	}))
	url := sidecar.URL
	sidecar.Close()
	t.Setenv("WATERMARKS_SYNTHID_TEXT_URL", url)
	handler := NewHTTPHandler("")
	status, body := postJSON(t, handler, "/watermark", map[string]any{"text": "hello"})
	if status != http.StatusBadGateway || body["ok"] != false {
		t.Fatalf("sidecar failure response = %d %#v", status, body)
	}

	request := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var spec map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	watermark := paths["/watermark"].(map[string]any)["post"].(map[string]any)
	responses := watermark["responses"].(map[string]any)
	if _, ok := paths["/watermark/batch"]; !ok {
		t.Fatal("OpenAPI is missing /watermark/batch")
	}
	if responses["502"].(map[string]any)["description"] == "Success" {
		t.Fatal("OpenAPI mislabeled watermark backend error as success")
	}
	requestBody := watermark["requestBody"].(map[string]any)
	schema := requestBody["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	options := schema["properties"].(map[string]any)["options"].(map[string]any)
	temperature := options["properties"].(map[string]any)["temperature"].(map[string]any)
	if temperature["exclusiveMinimum"] != true {
		t.Fatalf("OpenAPI exclusiveMinimum = %#v", temperature["exclusiveMinimum"])
	}
}

func TestTextWatermarkTimeoutResolutionPreservesExplicitDeadline(t *testing.T) {
	t.Setenv("WATERMARKS_SYNTHID_TEXT_TIMEOUT", "0.01")
	if got := textWatermarkTimeout(1500 * time.Millisecond); got != 1500*time.Millisecond {
		t.Fatalf("explicit timeout = %s, want 1.5s", got)
	}
	if got := textWatermarkTimeout(0); got != time.Second {
		t.Fatalf("environment timeout clamp = %s, want 1s", got)
	}
}
