package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var Version = "0.1.0"

func serverVersion() string {
	if version := strings.TrimSpace(os.Getenv("WATERMARKS_SERVER_VERSION")); version != "" {
		return version
	}
	return Version
}

type apiServer struct {
	apiKey   string
	maxBatch int
}

type apiRequest struct {
	File    string          `json:"file"`
	Data    string          `json:"data"`
	Name    string          `json:"name"`
	Detect  bool            `json:"detect"`
	Options json.RawMessage `json:"options"`
	Files   []apiRequest    `json:"files"`
}

type apiResponse struct {
	OK         bool   `json:"ok"`
	Name       string `json:"name,omitempty"`
	Kind       Kind   `json:"kind,omitempty"`
	Format     string `json:"format,omitempty"`
	Suspicious any    `json:"suspicious,omitempty"`
	// Report is a FileReport for inspect/detect and a CleanResult-shaped
	// object for clean, matching the upstream HTTP contract. Result remains
	// available on clean responses for callers that prefer the typed Go view.
	Report       any            `json:"report,omitempty"`
	Result       *CleanResult   `json:"result,omitempty"`
	Cleaned      string         `json:"cleaned,omitempty"`
	Detections   *[]any         `json:"detections,omitempty"`
	Results      []apiResponse  `json:"results,omitempty"`
	Errors       int            `json:"errors,omitempty"`
	Error        string         `json:"error,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
}

func detectionField(values []any) *[]any {
	if values == nil {
		return nil
	}
	return &values
}

var errAPIRequestTooLarge = errors.New("request body exceeds configured limit")

// NewHTTPHandler returns the HTTP API handler. If apiKey is non-empty, every
// endpoint, including /health, requires Authorization: Bearer <apiKey>.
func NewHTTPHandler(apiKey string) http.Handler {
	if strings.TrimSpace(apiKey) == "" {
		apiKey = strings.TrimSpace(os.Getenv("WATERMARKS_SERVER_API_KEY"))
	}
	maxBatch := 50
	if raw := os.Getenv("WATERMARKS_MAX_BATCH_FILES"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxBatch = n
		}
	}
	s := &apiServer{apiKey: apiKey, maxBatch: maxBatch}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/capabilities", s.capabilities)
	mux.HandleFunc("/openapi.json", s.openapi)
	mux.HandleFunc("/inspect", s.inspect)
	mux.HandleFunc("/detect", s.detect)
	mux.HandleFunc("/clean", s.clean)
	mux.HandleFunc("/inspect/batch", s.inspectBatch)
	mux.HandleFunc("/detect/batch", s.detectBatch)
	mux.HandleFunc("/clean/batch", s.cleanBatch)
	mux.HandleFunc("/watermark", s.watermark)
	mux.HandleFunc("/watermark/batch", s.watermarkBatch)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.authorized(r) {
			writeAPIJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "unauthorized"})
			return
		}
		if !knownAPIPath(r.URL.Path) {
			writeAPIJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "not found"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func knownAPIPath(path string) bool {
	switch path {
	case "/health", "/capabilities", "/openapi.json", "/inspect", "/detect", "/clean", "/inspect/batch", "/detect/batch", "/clean/batch", "/watermark", "/watermark/batch":
		return true
	default:
		return false
	}
}

// Serve starts the optional HTTP service and shuts it down when ctx is
// cancelled. The defaults match the upstream service: 127.0.0.1:8765.
func Serve(ctx context.Context, host string, port int) error {
	return ServeWithOptions(ctx, host, port, "", "")
}

// ServeWithOptions is the CLI-facing form of Serve. apiKey and
// strategyConfig override their environment/config defaults when supplied.
// The strategy config is scoped to the server lifetime because Layer B is also
// callable through the byte-oriented API.
func ServeWithOptions(ctx context.Context, host string, port int, apiKey, strategyConfig string) error {
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 8765
	}
	if apiKey == "" {
		apiKey = os.Getenv("WATERMARKS_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("WATERMARKS_SERVER_API_KEY")
	}
	var oldStrategyConfig string
	var hadStrategyConfig bool
	if strategyConfig != "" {
		oldStrategyConfig, hadStrategyConfig = os.LookupEnv("WATERMARKS_CLEAN_STRATEGY_FILE")
		if err := os.Setenv("WATERMARKS_CLEAN_STRATEGY_FILE", strategyConfig); err != nil {
			return fmt.Errorf("set strategy config: %w", err)
		}
		defer func() {
			if hadStrategyConfig {
				_ = os.Setenv("WATERMARKS_CLEAN_STRATEGY_FILE", oldStrategyConfig)
			} else {
				_ = os.Unsetenv("WATERMARKS_CLEAN_STRATEGY_FILE")
			}
		}()
	}
	srv := &http.Server{
		Addr:              net.JoinHostPort(host, strconv.Itoa(port)),
		Handler:           NewHTTPHandler(apiKey),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *apiServer) authorized(r *http.Request) bool {
	if s.apiKey == "" {
		return true
	}
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	return strings.HasPrefix(value, prefix) && strings.TrimSpace(strings.TrimPrefix(value, prefix)) == s.apiKey
}

func (s *apiServer) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{"ok": true, "version": serverVersion()})
}

func (s *apiServer) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	ghostscript := false
	if path, err := findGhostscript(); err == nil {
		ghostscript = commandUsable(filepath.Base(path))
	}
	configured := DefaultOptions()
	configured.MarkLLMDir = strings.TrimSpace(os.Getenv("MARKLLM_DIR"))
	configured.MarkLLMScheme = strings.TrimSpace(os.Getenv("WATERMARKS_MARKLLM_SCHEME"))
	ctrlregen := strings.TrimSpace(ctrlregenDirectory(configured)) != "" && externalAdapterAvailable("clean_ctrlregen.py", configured)
	diffusion := strings.TrimSpace(markDiffusionDirectory(configured)) != "" && externalAdapterAvailable("markdiffusion_harness.py", configured)
	video := externalAdapterAvailable("clean_video.py", configured) && (ctrlregen || diffusion)
	synthidLocal := strings.TrimSpace(os.Getenv("REVERSE_SYNTHID_DIR")) != "" && externalAdapterAvailable("score_synthid.py", configured)
	markllm := strings.TrimSpace(os.Getenv("MARKLLM_DIR")) != "" && externalAdapterAvailable("detect_text_watermark.py", configured)
	synthidText := strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_TEXT_URL")) != ""
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": serverVersion(),
		"kinds": map[string]any{
			"text":      []string{"Layer A Unicode inspection/cleaning", "NFKC normalization", "homoglyph normalization"},
			"image":     []string{"PNG", "JPEG", "WebP", "AVIF", "HEIC", "BMP", "GIF", "TIFF"},
			"container": []string{"SVG", "PDF", "DOCX", "XLSX", "PPTX", "ODT", "EPUB", "HTML", "Markdown", "LaTeX"},
			"av":        []string{"MP4", "MOV", "M4A", "M4V", "WAV", "MP3", "FLAC"},
		},
		"options": []string{"nfkc", "aggressive_homoglyphs", "normalize_spaces", "strip_emoji_glue", "strip_bidi", "keep_non_ai_metadata", "strip_all_metadata", "also_layer_a_text", "layer_a_only", "remove_pixel", "remove_audio_watermark", "deep_images", "detect_before", "detect_after", "style", "strategy", "backend", "model", "base_url", "reasoning_effort", "allow_remote", "as", "force_text", "stylometry", "threshold", "upstream_scripts", "synthid_dir", "markllm_scheme", "markllm_dir", "markllm_model", "markllm_timeout", "ctrlregen_dir", "ctrlregen_intensity", "ctrlregen_steps", "ctrlregen_device", "ctrlregen_seed", "ctrlregen_timeout", "markdiffusion_dir", "markdiffusion_intensity", "markdiffusion_model", "markdiffusion_size", "markdiffusion_steps", "markdiffusion_device", "markdiffusion_timeout", "vote_threshold", "frame_fraction", "audio_tempo", "audio_pitch", "audio_bitrate", "audio_codec", "ffmpeg_timeout"},
		"tools": map[string]bool{
			"c2patool":    commandUsable("c2patool"),
			"exiftool":    commandUsable("exiftool"),
			"qpdf":        commandUsable("qpdf"),
			"ghostscript": ghostscript,
			"ffmpeg":      commandUsable("ffmpeg"),
		},
		"pixel_backends": map[string]bool{"ctrlregen": ctrlregen, "diffusion": diffusion, "video": video},
		"scorers": map[string]bool{
			"stylometry":    true,
			"gumbel":        strings.TrimSpace(os.Getenv("WATERMARKS_GUMBEL_KEY")) != "",
			"synthid":       strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_SCORER_URL")) != "" || synthidLocal,
			"synthid_http":  strings.TrimSpace(os.Getenv("WATERMARKS_SYNTHID_SCORER_URL")) != "",
			"synthid_local": synthidLocal,
		},
		"text_detectors": map[string]bool{
			"markllm":     markllm,
			"gumbel":      strings.TrimSpace(os.Getenv("WATERMARKS_GUMBEL_KEY")) != "",
			"claude-text": false,
		},
		"text_generators":  map[string]bool{"synthid_http": synthidText, "markllm": strings.TrimSpace(os.Getenv("MARKLLM_DIR")) != ""},
		"harnesses":        map[string]bool{"markllm": markllm},
		"rewrite_backends": map[string]bool{"print-prompt": true, "ollama": true, "openai-compatible": true, "mlm": externalAdapterAvailable("rewrite_text.py", configured)},
		"endpoints":        []string{"/health", "/capabilities", "/openapi.json", "/inspect", "/detect", "/clean", "/inspect/batch", "/detect/batch", "/clean/batch", "/watermark", "/watermark/batch"},
	})
}

func externalAdapterAvailable(scriptName string, opts Options) bool {
	_, scriptsDir, err := externalScript(scriptName, opts)
	return err == nil && externalPython(scriptsDir) != ""
}

func (s *apiServer) openapi(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	paths := map[string]any{
		"/health":          map[string]any{"get": map[string]any{"summary": "liveness and version", "responses": apiResponses("healthy")}},
		"/capabilities":    map[string]any{"get": map[string]any{"summary": "available tools and backends", "responses": apiResponses("capabilities")}},
		"/inspect":         apiPostOperation("inspect file; body.file is base64", "inspection report"),
		"/detect":          apiPostOperation("detect file format and markers", "detections"),
		"/clean":           apiPostOperation("clean one file; response.cleaned is base64", "cleaned file"),
		"/inspect/batch":   apiBatchPostOperation("inspect up to WATERMARKS_MAX_BATCH_FILES files", "reports"),
		"/detect/batch":    apiBatchPostOperation("detect up to WATERMARKS_MAX_BATCH_FILES files", "detections"),
		"/clean/batch":     apiBatchPostOperation("clean up to WATERMARKS_MAX_BATCH_FILES files", "results"),
		"/watermark":       apiWatermarkOperation(false),
		"/watermark/batch": apiWatermarkOperation(true),
	}
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title": "aiwr service", "version": serverVersion(),
			"description": "Native Go aiwr inspection and cleaning service. Optional research backends are external adapters. File bytes are base64 encoded.",
		},
		"paths": paths,
	}
	if s.apiKey != "" {
		spec["components"] = map[string]any{"securitySchemes": map[string]any{
			"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
		}}
		spec["security"] = []any{map[string]any{"bearerAuth": []string{}}}
	}
	writeAPIJSON(w, http.StatusOK, spec)
}

func watermarkRequestSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Prompt or text to watermark",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Base64-encoded text file",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Optional filename",
			},
			"keys": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Optional SynthID key sequence",
			},
			"options": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"scheme":         map[string]any{"type": "string"},
					"seed":           map[string]any{"type": "integer"},
					"max_new_tokens": map[string]any{"type": "integer", "minimum": 1, "maximum": 8192},
					"min_length":     map[string]any{"type": "integer", "minimum": 0, "maximum": 8192},
					"temperature":    map[string]any{"type": "number", "minimum": 0, "exclusiveMinimum": true},
					"top_p":          map[string]any{"type": "number", "minimum": 0, "exclusiveMinimum": true, "maximum": 1},
					"model":          map[string]any{"type": "string"},
					"device":         map[string]any{"type": "string", "description": "Inference device, for example cpu or cuda:0"},
					"config":         map[string]any{"type": "string", "description": "Path to a custom watermark config file"},
					"offline":        map[string]any{"type": "boolean", "default": false, "description": "Disable network access for model loading"},
				},
			},
		},
	}
}

func watermarkErrorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":         map[string]any{"type": "boolean", "enum": []bool{false}},
			"kind":       map[string]any{"type": "string", "enum": []string{"text"}},
			"error":      map[string]any{"type": "string"},
			"error_code": map[string]any{"type": "string"},
		},
	}
}

func apiWatermarkOperation(batch bool) map[string]any {
	requestSchema := watermarkRequestSchema()
	var bodySchema map[string]any
	var summary string
	if batch {
		summary = "Generate watermarked text for a batch of prompts"
		bodySchema = map[string]any{
			"type":     "object",
			"required": []string{"files"},
			"properties": map[string]any{
				"files": map[string]any{
					"type":  "array",
					"items": requestSchema,
				},
			},
		}
	} else {
		summary = "Generate watermarked text using the configured sidecar or generator"
		bodySchema = requestSchema
	}
	responseSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":               map[string]any{"type": "boolean"},
			"kind":             map[string]any{"type": "string", "enum": []string{"text"}},
			"watermarked_text": map[string]any{"type": "string"},
			"report":           map[string]any{"type": "object"},
			"results":          map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		},
	}
	errorSchema := watermarkErrorSchema()
	return map[string]any{
		"post": map[string]any{
			"summary": summary,
			"requestBody": map[string]any{
				"required": true,
				"content":  map[string]any{"application/json": map[string]any{"schema": bodySchema}},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Success",
					"content":     map[string]any{"application/json": map[string]any{"schema": responseSchema}},
				},
				"400": map[string]any{
					"description": "Bad request",
					"content":     map[string]any{"application/json": map[string]any{"schema": errorSchema}},
				},
				"401": map[string]any{
					"description": "Missing or invalid bearer token",
					"content":     map[string]any{"application/json": map[string]any{"schema": errorSchema}},
				},
				"502": map[string]any{
					"description": "Sidecar or generator backend error",
					"content":     map[string]any{"application/json": map[string]any{"schema": errorSchema}},
				},
				"503": map[string]any{
					"description": "No text watermark generator configured",
					"content":     map[string]any{"application/json": map[string]any{"schema": errorSchema}},
				},
			},
		},
	}
}

func apiPostOperation(description, responseDescription string) map[string]any {
	return map[string]any{"post": map[string]any{
		"summary": description,
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]any{
					"type":     "object",
					"required": []string{"file"},
					"properties": map[string]any{
						"file":    map[string]any{"type": "string", "description": "base64-encoded file bytes"},
						"data":    map[string]any{"type": "string", "description": "base64 alias for file"},
						"name":    map[string]any{"type": "string"},
						"detect":  map[string]any{"type": "boolean"},
						"options": map[string]any{"type": "object"},
						"files":   map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					},
				},
			}},
		},
		"responses": apiResponses(responseDescription),
	}}
}

func apiBatchPostOperation(description, responseDescription string) map[string]any {
	return map[string]any{"post": map[string]any{
		"summary": description,
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]any{
					"type":     "object",
					"required": []string{"files"},
					"properties": map[string]any{
						"files": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":     "object",
								"required": []string{"file"},
								"properties": map[string]any{
									"file":    map[string]any{"type": "string"},
									"name":    map[string]any{"type": "string"},
									"detect":  map[string]any{"type": "boolean"},
									"options": map[string]any{"type": "object"},
								},
							},
						},
						"detect": map[string]any{
							"type":        "boolean",
							"description": "also run configured text watermark detectors for inspect batches",
						},
					},
				},
			}},
		},
		"responses": apiResponses(responseDescription),
	}}
}

func apiResponses(success string) map[string]any {
	return map[string]any{
		"200": map[string]any{"description": success},
		"400": map[string]any{"description": "invalid request"},
		"401": map[string]any{"description": "missing or invalid bearer token"},
		"404": map[string]any{"description": "not found"},
		"413": map[string]any{"description": "request body too large"},
		"500": map[string]any{"description": "internal server error"},
	}
}

func (s *apiServer) inspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	req, data, opts, err := readAPIRequest(r)
	if err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	opts.Stylometry = true
	report, err := InspectBytes(data, req.Name, opts)
	if err != nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	var detections []any
	if req.Detect && report.Kind == KindText {
		detections = apiDetectReport(data, report, opts)
	}
	writeAPIJSON(w, http.StatusOK, apiResponse{OK: true, Kind: report.Kind, Format: report.Format, Suspicious: suspiciousEvidence(report, detections, opts.Threshold), Report: inspectAPIReport(report, detections), Detections: detectionField(detections)})
}

func (s *apiServer) detect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	req, data, opts, err := readAPIRequest(r)
	if err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	opts.Stylometry = true
	report, err := InspectBytes(data, req.Name, opts)
	if err != nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	detections := apiDetectReport(data, report, opts)
	writeAPIJSON(w, http.StatusOK, apiResponse{OK: true, Kind: report.Kind, Format: report.Format, Suspicious: suspiciousEvidence(report, detections, opts.Threshold), Detections: detectionField(detections), Report: inspectAPIReport(report, detections)})
}

func (s *apiServer) clean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	req, data, opts, err := readAPIRequest(r)
	if err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	result, cleaned, _, err := cleanServerBytes(data, req.Name, opts)
	if err != nil {
		writeAPIJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	cleanReport := cleanAPIReport(result, cleaned)
	writeAPIJSON(w, http.StatusOK, apiResponse{OK: true, Name: req.Name, Kind: result.Kind, Format: result.Format, Report: &cleanReport, Result: &result, Cleaned: base64.StdEncoding.EncodeToString(cleaned)})
}

func (s *apiServer) inspectBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	reqs, err := readBatchRequest(r, s.maxBatch)
	if err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	results := make([]apiResponse, 0, len(reqs))
	errorsCount := 0
	for _, req := range reqs {
		data, decodeErr := decodeAPIData(req)
		if decodeErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: decodeErr.Error()})
			continue
		}
		opts, optionErr := optionsFromJSON(req.Options)
		if optionErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: optionErr.Error()})
			continue
		}
		opts.Stylometry = true
		report, inspectErr := InspectBytes(data, req.Name, opts)
		if inspectErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: inspectErr.Error()})
			continue
		}
		var detections []any
		if req.Detect && report.Kind == KindText {
			detections = apiDetectReport(data, report, opts)
		}
		results = append(results, apiResponse{OK: true, Name: req.Name, Kind: report.Kind, Format: report.Format, Suspicious: suspiciousEvidence(report, detections, opts.Threshold), Detections: detectionField(detections), Report: inspectAPIReport(report, detections)})
	}
	writeAPIJSON(w, http.StatusOK, apiResponse{OK: true, Results: results, Errors: errorsCount})
}

func (s *apiServer) detectBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	reqs, err := readBatchRequest(r, s.maxBatch)
	if err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	results := make([]apiResponse, 0, len(reqs))
	errorsCount := 0
	for _, req := range reqs {
		data, decodeErr := decodeAPIData(req)
		if decodeErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: decodeErr.Error()})
			continue
		}
		opts, optionErr := optionsFromJSON(req.Options)
		if optionErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: optionErr.Error()})
			continue
		}
		opts.Stylometry = true
		report, inspectErr := InspectBytes(data, req.Name, opts)
		if inspectErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: inspectErr.Error()})
			continue
		}
		detections := apiDetectReport(data, report, opts)
		results = append(results, apiResponse{
			OK: true, Name: req.Name, Kind: report.Kind, Format: report.Format,
			Suspicious: suspiciousEvidence(report, detections, opts.Threshold),
			Detections: detectionField(detections), Report: inspectAPIReport(report, detections),
		})
	}
	writeAPIJSON(w, http.StatusOK, apiResponse{OK: true, Results: results, Errors: errorsCount})
}

func (s *apiServer) cleanBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIJSON(w, http.StatusMethodNotAllowed, apiResponse{OK: false, Error: "method not allowed"})
		return
	}
	reqs, err := readBatchRequest(r, s.maxBatch)
	if err != nil {
		writeAPIJSON(w, apiErrorStatus(err), apiResponse{OK: false, Error: err.Error()})
		return
	}
	results := make([]apiResponse, 0, len(reqs))
	errorsCount := 0
	for _, req := range reqs {
		data, decodeErr := decodeAPIData(req)
		if decodeErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: decodeErr.Error()})
			continue
		}
		opts, optionErr := optionsFromJSON(req.Options)
		if optionErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: optionErr.Error()})
			continue
		}
		result, cleaned, _, cleanErr := cleanServerBytes(data, req.Name, opts)
		if cleanErr != nil {
			errorsCount++
			results = append(results, apiResponse{OK: false, Name: req.Name, Error: cleanErr.Error()})
			continue
		}
		cleanReport := cleanAPIReport(result, cleaned)
		results = append(results, apiResponse{OK: true, Name: req.Name, Kind: result.Kind, Format: result.Format, Report: &cleanReport, Result: &result, Cleaned: base64.StdEncoding.EncodeToString(cleaned)})
	}
	writeAPIJSON(w, http.StatusOK, apiResponse{OK: true, Results: results, Errors: errorsCount})
}

// cleanAPIReport keeps /clean compatible with the upstream endpoint while
// retaining the richer Go clean-result fields. The upstream text report has a
// few fields that are not part of CleanResult (notably length, layer_b, and
// text_detectors), so they are added here rather than forcing callers to
// inspect the Go-specific Result extension.
func cleanAPIReport(result CleanResult, cleaned []byte) map[string]any {
	result.Input = ""
	result.Output = ""
	encoded, _ := json.Marshal(result)
	var report map[string]any
	if err := json.Unmarshal(encoded, &report); err != nil || report == nil {
		report = map[string]any{
			"kind": result.Kind, "changed": result.Changed,
			"bytes_in": result.BytesIn, "bytes_out": result.BytesOut,
		}
	}
	if result.Kind != KindText {
		// The upstream image/AV/container cleaners always expose the residual
		// booleans and findings, including false/empty values. CleanResult uses
		// omitempty for the Go-facing API, so restore those stable HTTP fields.
		report["still_has_c2pa"] = result.StillHasC2PA
		report["still_has_ai_metadata"] = result.StillHasAI
		postFindings := result.PostFindings
		if postFindings == nil {
			postFindings = []string{}
		}
		report["post_findings"] = postFindings
		if result.Kind == KindImage {
			// These are part of the upstream image report even when an optional
			// scorer/backend was not configured (where they serialize as null).
			report["synthid_before"] = result.SynthIDBefore
			report["synthid_after"] = result.SynthIDAfter
			report["pixel_removal"] = result.PixelRemoval
		}
		if result.Kind == KindContainer {
			report["meta"] = map[string]any{"format": result.Format}
		}
		return report
	}

	report["length"] = utf8.RuneCount(cleaned)
	if layerB, ok := result.Stats["layer_b"]; ok {
		report["layer_b"] = layerB
	}
	textDetectors := map[string]any{}
	if before, ok := result.Stats["detect_before"].([]any); ok {
		textDetectors["before"] = configuredTextDetections(before)
	}
	if after, ok := result.Stats["detect_after"].([]any); ok {
		textDetectors["after"] = configuredTextDetections(after)
	}
	if len(textDetectors) > 0 {
		report["text_detectors"] = textDetectors
	}
	return report
}

func inspectAPIReport(report FileReport, detections []any) any {
	if report.Kind == KindUnknown {
		note := "unrecognized format; use a filename with a known extension"
		if len(report.Notes) > 0 && strings.TrimSpace(report.Notes[0]) != "" {
			note = report.Notes[0]
		}
		return map[string]any{"note": note}
	}
	if report.Kind == KindText && report.Text != nil {
		textReport := map[string]any{
			"length":           report.Text.Length,
			"suspicious_total": report.Text.SuspiciousTotal,
			"hits":             report.Text.Hits,
			"notes":            report.Text.Notes,
		}
		if report.Text.Stylometry != nil {
			textReport["stylometry"] = report.Text.Stylometry
		}
		if detections != nil {
			textReport["text_detectors"] = configuredTextDetections(detections)
		}
		return textReport
	}
	return &report
}

// configuredTextDetections mirrors the upstream inspect endpoint: its
// text_detectors field contains the configured detector registry, while
// stylometry remains a separate report section. The Go API still exposes the
// richer combined detector list at the response's top-level detections field.
func configuredTextDetections(detections []any) []any {
	filtered := make([]any, 0, len(detections))
	for _, detection := range detections {
		values, ok := detection.(map[string]any)
		if ok {
			if name, _ := values["detector"].(string); name == "stylometry" {
				continue
			}
		}
		filtered = append(filtered, detection)
	}
	return filtered
}

// apiDetectReport follows the upstream service detector surface. Its /detect
// endpoint exposes the SynthID scorer for images and an empty detector list for
// AV/container inputs; metadata inspection remains available in the separate
// report. The richer detectReport is retained for the Go CLI.
func apiDetectReport(data []byte, report FileReport, opts Options) []any {
	switch report.Kind {
	case KindImage:
		if report.SynthID != nil {
			return []any{report.SynthID}
		}
		return []any{map[string]any{
			"detector": "synthid", "available": false,
			"error": "no SynthID scorer configured (set WATERMARKS_SYNTHID_SCORER_URL or REVERSE_SYNTHID_DIR)",
		}}
	case KindAV, KindContainer, KindUnknown:
		return []any{}
	default:
		return detectReport(data, report, opts)
	}
}

func cleanServerBytes(data []byte, name string, opts Options) (CleanResult, []byte, FileReport, error) {
	if opts.RemovePixel != "" && opts.RemoveAudioWatermark {
		return CleanResult{}, nil, FileReport{}, errors.New("remove_pixel and remove_audio_watermark cannot be combined")
	}
	var before []any
	if opts.DetectBefore {
		report, err := InspectBytes(data, name, opts)
		if err != nil {
			return CleanResult{}, nil, FileReport{}, err
		}
		before = apiDetectReport(data, report, opts)
	}

	// The HTTP API treats optional pixel purification as a best-effort
	// capability: metadata cleaning must still succeed when the user's GPU
	// backend is absent.  The file CLI keeps the stricter error contract.
	requestedPixel := opts.RemovePixel
	pixelApplied := false
	needFinalReport := false
	metadataOpts := opts
	metadataOpts.RemovePixel = ""
	result, cleaned, postReport, err := cleanBytesWithReport(data, name, metadataOpts)
	if err != nil {
		return CleanResult{}, nil, FileReport{}, err
	}
	if requestedPixel != "" {
		if result.Kind != KindImage && !(result.Kind == KindAV && isVideoName(name)) {
			return CleanResult{}, nil, FileReport{}, errors.New("remove_pixel requires an image or video input")
		}
		pixelOpts := opts
		pixelOpts.RemovePixel = requestedPixel
		tempDest, tempErr := os.CreateTemp("", "aiwr-http-pixel-output-*"+filepath.Ext(name))
		if tempErr != nil {
			return CleanResult{}, nil, FileReport{}, tempErr
		}
		tempPath := tempDest.Name()
		if closeErr := tempDest.Close(); closeErr != nil {
			_ = os.Remove(tempPath)
			return CleanResult{}, nil, FileReport{}, closeErr
		}
		defer os.Remove(tempPath)
		pixelCleaned, pixelReport, pixelErr := runPixelBackend(cleaned, name, tempPath, 0o600, pixelOpts)
		result.PixelRemoval = pixelReport
		if pixelErr != nil {
			if pixelReport == nil || pixelReport["available"] != false {
				return CleanResult{}, nil, FileReport{}, pixelErr
			}
			result.Actions = append(result.Actions, "per-frame video purification skipped: "+pixelErrorMessage(pixelReport, pixelErr))
		} else {
			cleaned = pixelCleaned
			pixelApplied = true
			needFinalReport = true
			result.Actions = append(result.Actions, "pixel watermark removal via "+requestedPixel)
			result.Changed = !sameBytes(data, cleaned)
			result.BytesOut = int64(len(cleaned))
		}
	}
	if result.Kind == KindText && !opts.LayerAOnly {
		rewritten, layerBStats, layerBErr := ApplyLayerB(string(cleaned), opts)
		if layerBErr != nil {
			return CleanResult{}, nil, FileReport{}, layerBErr
		}
		candidate := []byte(rewritten)
		result.Actions = append(result.Actions, "Layer B text rewrite")
		if result.Stats == nil {
			result.Stats = map[string]any{}
		}
		result.Stats["layer_b"] = layerBStats
		cleaned = candidate
		needFinalReport = true
		result.Changed = !sameBytes(data, cleaned)
		result.BytesOut = int64(len(cleaned))
	}
	if opts.RemoveAudioWatermark {
		if result.Kind != KindAV || !isServerAudioName(name) {
			return CleanResult{}, nil, FileReport{}, errors.New("remove_audio_watermark is supported for WAV, MP3, FLAC, M4A, AAC, OGG, and Opus inputs; video streams are not remixed")
		}
		remixed, remixErr := serverAudioRemix(cleaned, name, opts)
		if remixErr != nil {
			result.AudioMarkRemoval = map[string]any{"available": false, "error": remixErr.Error()}
			result.Actions = append(result.Actions, "destructive audio watermark chain skipped: "+remixErr.Error())
		} else {
			cleaned = remixed
			needFinalReport = true
			result.AudioMarkRemoval = map[string]any{
				"available": true,
				"tempo":     opts.AudioTempo, "pitch_semitones": opts.AudioPitch,
				"bitrate": opts.AudioBitrate, "codec": audioOutputCodec(opts),
			}
			result.Actions = append(result.Actions, "destructive audio watermark chain via ffmpeg")
			result.Changed = !sameBytes(data, cleaned)
			result.BytesOut = int64(len(cleaned))
		}
		if result.Stats == nil {
			result.Stats = map[string]any{}
		}
		result.Stats["audio_remix"] = result.AudioMarkRemoval
	}
	if needFinalReport {
		if result.Kind == KindImage {
			// Image cleanup's post-pass is deliberately byte-only: the optional
			// local tools and SynthID scorer already ran before cleaning, and a
			// second normal inspect would make HTTP cleanup reread those tools.
			postReport = inspectImageResidualWithOptions(cleaned, name, opts)
			if pixelApplied {
				result.SynthIDAfter = detectSynthIDForOptions(cleaned, opts)
			}
		} else {
			postReport, err = InspectBytes(cleaned, name, opts)
		}
		if err != nil {
			return CleanResult{}, nil, FileReport{}, err
		}
		applyPostReport(&result, postReport, cleaned)
	}
	if opts.RemoveAudioWatermark {
		result.Format = postReport.Format
		result.BytesOut = int64(len(cleaned))
	}
	if opts.DetectBefore || opts.DetectAfter {
		if result.Stats == nil {
			result.Stats = map[string]any{}
		}
		if opts.DetectBefore {
			result.Stats["detect_before"] = before
		}
		if opts.DetectAfter {
			result.Stats["detect_after"] = apiDetectReport(cleaned, postReport, opts)
		}
	}
	return result, cleaned, postReport, nil
}

func pixelErrorMessage(report map[string]any, fallback error) string {
	if report != nil {
		if message, ok := report["error"].(string); ok && strings.TrimSpace(message) != "" {
			return message
		}
	}
	return fallback.Error()
}

func audioOutputCodec(opts Options) string {
	if codec := strings.TrimSpace(opts.AudioCodec); codec != "" {
		return codec
	}
	return "aac"
}

func isServerAudioName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav", ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus":
		return true
	default:
		return false
	}
}

func serverAudioRemix(data []byte, name string, opts Options) ([]byte, error) {
	tempDir, err := os.MkdirTemp("", "aiwr-audio-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	ext := strings.ToLower(filepath.Ext(name))
	source := filepath.Join(tempDir, "source"+ext)
	dest := filepath.Join(tempDir, "remixed.m4a")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		return nil, err
	}
	if hasVideo, probeErr := mediaHasVideo(source); probeErr != nil {
		return nil, probeErr
	} else if hasVideo {
		return nil, fmt.Errorf("audio watermark removal refuses media with a video stream: %s", name)
	}
	if err := runAudioRemix(source, dest, opts); err != nil {
		return nil, err
	}
	return os.ReadFile(dest)
}

func readAPIRequest(r *http.Request) (apiRequest, []byte, Options, error) {
	var req apiRequest
	if err := decodeJSONBody(r, &req); err != nil {
		return req, nil, Options{}, err
	}
	data, err := decodeAPIData(req)
	if err != nil {
		return req, nil, Options{}, err
	}
	if req.Name == "" {
		req.Name = "input"
	} else {
		req.Name = safeAPIName(req.Name)
	}
	opts, err := optionsFromJSON(req.Options)
	if err != nil {
		return req, nil, Options{}, err
	}
	return req, data, opts, nil
}

func readBatchRequest(r *http.Request, max int) ([]apiRequest, error) {
	var body struct {
		Files  []apiRequest `json:"files"`
		Items  []apiRequest `json:"items"`
		Detect bool         `json:"detect"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		return nil, err
	}
	files := body.Files
	if len(files) == 0 {
		files = body.Items
	}
	if len(files) == 0 {
		return nil, errors.New("request must contain a non-empty files array")
	}
	if len(files) > max {
		return nil, fmt.Errorf("batch contains %d files; limit is %d", len(files), max)
	}
	for i := range files {
		if body.Detect {
			files[i].Detect = true
		}
		if files[i].Name == "" {
			files[i].Name = "input"
		} else {
			files[i].Name = safeAPIName(files[i].Name)
		}
	}
	return files, nil
}

func safeAPIName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "" || name == string(filepath.Separator) {
		return "input"
	}
	return name
}

func decodeJSONBody(r *http.Request, target any) error {
	limit := maxInputBytes() + maxInputBytes()/2
	r.Body = http.MaxBytesReader(nil, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errAPIRequestTooLarge
		}
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errAPIRequestTooLarge
		}
		if err == nil {
			return errors.New("invalid JSON: trailing data")
		}
		return fmt.Errorf("invalid JSON after request body: %w", err)
	}
	return nil
}

func apiErrorStatus(err error) int {
	if errors.Is(err, errAPIRequestTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func decodeAPIData(req apiRequest) ([]byte, error) {
	encoded := strings.TrimSpace(req.File)
	if encoded == "" {
		encoded = strings.TrimSpace(req.Data)
	}
	if encoded == "" {
		return nil, errors.New("request must contain base64 file data in file or data")
	}
	var data []byte
	var err error
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		data, err = encoding.DecodeString(encoded)
		if err == nil {
			if int64(len(data)) > maxInputBytes() {
				return nil, fmt.Errorf("decoded input exceeds %d bytes", maxInputBytes())
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("invalid base64 file data: %w", err)
}

func optionsFromJSON(raw json.RawMessage) (Options, error) {
	opts := DefaultOptions()
	if len(raw) == 0 || string(raw) == "null" {
		return opts, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return opts, fmt.Errorf("invalid options object: %w", err)
	}
	known := map[string]bool{
		"nfkc": true, "aggressive": true, "aggressive_homoglyphs": true,
		"normalize_spaces": true, "strip_emoji_glue": true, "strip_bidi": true,
		"keep_non_ai_metadata": true, "force_text": true, "as": true,
		"also_layer_a_text": true, "layer_a_only": true, "strip_all_metadata": true,
		"remove_audio_watermark": true, "remove_pixel": true,
		"deep_images": true, "detect": true, "detect_before": true, "detect_after": true,
		"stylometry": true, "threshold": true,
		"strategy": true, "backend": true, "model": true, "base_url": true,
		"style": true, "temperature": true, "timeout": true, "reasoning_effort": true, "allow_remote": true,
		"upstream_scripts": true, "synthid_dir": true, "markllm_scheme": true, "markllm_dir": true,
		"markllm_model": true, "markllm_timeout": true,
		"ctrlregen_dir": true, "ctrlregen_intensity": true, "ctrlregen_steps": true,
		"ctrlregen_device": true, "ctrlregen_seed": true, "ctrlregen_timeout": true,
		"markdiffusion_dir": true, "markdiffusion_intensity": true, "markdiffusion_model": true,
		"markdiffusion_size": true, "markdiffusion_steps": true, "markdiffusion_device": true,
		"markdiffusion_timeout": true, "vote_threshold": true, "frame_fraction": true,
		"audio_tempo": true, "audio_pitch": true, "audio_bitrate": true, "audio_codec": true,
		"ffmpeg_timeout": true,
	}
	for key := range values {
		if !known[key] {
			return opts, fmt.Errorf("unknown option: %s", key)
		}
	}
	readBool := func(names ...string) (bool, bool, error) {
		for _, name := range names {
			if value, ok := values[name]; ok {
				switch strings.TrimSpace(string(value)) {
				case "true":
					return true, true, nil
				case "false":
					return false, true, nil
				default:
					return false, true, fmt.Errorf("option '%s' must be a boolean", name)
				}
			}
		}
		return false, false, nil
	}
	readString := func(name string, target *string) error {
		if value, ok := values[name]; ok {
			if strings.TrimSpace(string(value)) == "null" || json.Unmarshal(value, target) != nil {
				return fmt.Errorf("option '%s' must be a string", name)
			}
		}
		return nil
	}
	var err error
	if opts.NFKC, _, err = readBool("nfkc"); err != nil {
		return opts, err
	}
	if opts.AggressiveHomoglyphs, _, err = readBool("aggressive", "aggressive_homoglyphs"); err != nil {
		return opts, err
	}
	if v, ok, e := readBool("normalize_spaces"); e != nil {
		return opts, e
	} else if ok {
		opts.NormalizeSpaces = v
	}
	if opts.StripEmojiGlue, _, err = readBool("strip_emoji_glue"); err != nil {
		return opts, err
	}
	if opts.StripBidi, _, err = readBool("strip_bidi"); err != nil {
		return opts, err
	}
	if opts.KeepNonAIMetadata, _, err = readBool("keep_non_ai_metadata"); err != nil {
		return opts, err
	}
	if opts.ForceText, _, err = readBool("force_text"); err != nil {
		return opts, err
	}
	if v, ok, e := readBool("also_layer_a_text"); e != nil {
		return opts, e
	} else if ok {
		opts.AlsoLayerAText = v
		// The upstream option controls both visible container bodies and the
		// optional post-Layer-B scrub when explicitly supplied.
		opts.LayerAAfter = v
	}
	if opts.LayerAOnly, _, err = readBool("layer_a_only"); err != nil {
		return opts, err
	}
	if opts.StripAllMetadata, _, err = readBool("strip_all_metadata"); err != nil {
		return opts, err
	}
	if _, ok := values["strip_all_metadata"]; ok {
		opts.StripAllMetadataSet = true
	}
	if opts.RemoveAudioWatermark, _, err = readBool("remove_audio_watermark"); err != nil {
		return opts, err
	}
	if opts.RewriteAllowRemote, _, err = readBool("allow_remote"); err != nil {
		return opts, err
	}
	if opts.DetectBefore, _, err = readBool("detect_before"); err != nil {
		return opts, err
	}
	if opts.DetectAfter, _, err = readBool("detect_after"); err != nil {
		return opts, err
	}
	if opts.Stylometry, _, err = readBool("stylometry"); err != nil {
		return opts, err
	}
	if value, ok := values["threshold"]; ok {
		if err := json.Unmarshal(value, &opts.Threshold); err != nil || opts.Threshold <= 0 || opts.Threshold > 1 {
			return opts, fmt.Errorf("option threshold must be a number in (0,1]")
		}
	}
	if _, ok := values["as"]; ok {
		if err := readString("as", &opts.ForceType); err != nil {
			return opts, err
		}
	}
	if _, ok := values["deep_images"]; ok {
		var deepMode string
		if err := readString("deep_images", &deepMode); err != nil {
			return opts, err
		}
		opts.DeepImages = DeepImages(deepMode)
		switch opts.DeepImages {
		case DeepAuto, DeepAlways, DeepLossless, DeepNever:
		default:
			return opts, fmt.Errorf("option deep_images must be auto, always, lossless, or never")
		}
	}
	if _, ok := values["remove_pixel"]; ok {
		if err := readString("remove_pixel", &opts.RemovePixel); err != nil {
			return opts, err
		}
		if opts.RemovePixel != "" && opts.RemovePixel != "ctrlregen" && opts.RemovePixel != "diffusion" {
			return opts, fmt.Errorf("option remove_pixel must be ctrlregen or diffusion")
		}
	}
	if err := readString("upstream_scripts", &opts.UpstreamScriptsDir); err != nil {
		return opts, err
	}
	if err := readString("synthid_dir", &opts.SynthIDDir); err != nil {
		return opts, err
	}
	if err := readString("markllm_scheme", &opts.MarkLLMScheme); err != nil {
		return opts, err
	}
	if opts.MarkLLMScheme != "" {
		switch strings.ToLower(opts.MarkLLMScheme) {
		case "kgw", "synthid", "synthid-text", "exp", "unigram", "sir":
			opts.MarkLLMScheme = strings.ToLower(opts.MarkLLMScheme)
		default:
			return opts, fmt.Errorf("option markllm_scheme has an unsupported scheme %q", opts.MarkLLMScheme)
		}
	}
	if err := readString("markllm_dir", &opts.MarkLLMDir); err != nil {
		return opts, err
	}
	if err := readString("markllm_model", &opts.MarkLLMModel); err != nil {
		return opts, err
	}
	if err := readString("ctrlregen_dir", &opts.CtrlRegenDir); err != nil {
		return opts, err
	}
	if err := readString("ctrlregen_device", &opts.CtrlRegenDevice); err != nil {
		return opts, err
	}
	if err := readString("markdiffusion_dir", &opts.MarkDiffusionDir); err != nil {
		return opts, err
	}
	if err := readString("markdiffusion_model", &opts.MarkDiffusionModel); err != nil {
		return opts, err
	}
	if err := readString("markdiffusion_device", &opts.MarkDiffusionDevice); err != nil {
		return opts, err
	}
	if err := readString("audio_bitrate", &opts.AudioBitrate); err != nil {
		return opts, err
	}
	if err := readString("audio_codec", &opts.AudioCodec); err != nil {
		return opts, err
	}
	jsonNumber := func(value json.RawMessage) (json.Number, bool) {
		dec := json.NewDecoder(strings.NewReader(string(value)))
		dec.UseNumber()
		token, err := dec.Token()
		if err != nil {
			return "", false
		}
		number, ok := token.(json.Number)
		if !ok {
			return "", false
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			return "", false
		}
		return number, true
	}
	readFloat := func(name string, target *float64, min, max float64) error {
		if value, ok := values[name]; ok {
			number, valid := jsonNumber(value)
			if !valid || json.Unmarshal([]byte(number), target) != nil || *target < min || *target > max {
				return fmt.Errorf("option %s must be a number in [%g,%g]", name, min, max)
			}
		}
		return nil
	}
	readInt := func(name string, target *int, min int) error {
		if value, ok := values[name]; ok {
			number, valid := jsonNumber(value)
			if !valid || json.Unmarshal([]byte(number), target) != nil || *target < min {
				return fmt.Errorf("option %s must be an integer >= %d", name, min)
			}
		}
		return nil
	}
	minInt := -int(^uint(0)>>1) - 1
	if err := readFloat("ctrlregen_intensity", &opts.CtrlRegenIntensity, 0, 1); err != nil {
		return opts, err
	}
	if opts.CtrlRegenIntensity <= 0 {
		return opts, errors.New("option ctrlregen_intensity must be in (0,1]")
	}
	if err := readInt("ctrlregen_steps", &opts.CtrlRegenSteps, 1); err != nil {
		return opts, err
	}
	if err := readInt("ctrlregen_seed", &opts.CtrlRegenSeed, minInt); err != nil {
		return opts, err
	}
	if _, ok := values["ctrlregen_seed"]; ok {
		opts.CtrlRegenSeedSet = true
	}
	if err := readInt("ctrlregen_timeout", &opts.CtrlRegenTimeout, 1); err != nil {
		return opts, err
	}
	if err := readFloat("markdiffusion_intensity", &opts.MarkDiffusionIntensity, 0, 1); err != nil {
		return opts, err
	}
	if opts.MarkDiffusionIntensity <= 0 {
		return opts, errors.New("option markdiffusion_intensity must be in (0,1]")
	}
	if err := readInt("markdiffusion_size", &opts.MarkDiffusionSize, 1); err != nil {
		return opts, err
	}
	if err := readInt("markdiffusion_steps", &opts.MarkDiffusionSteps, 1); err != nil {
		return opts, err
	}
	if err := readInt("markdiffusion_timeout", &opts.MarkDiffusionTimeout, 1); err != nil {
		return opts, err
	}
	if err := readFloat("vote_threshold", &opts.VideoVoteThreshold, 0, 1); err != nil {
		return opts, err
	}
	if opts.VideoVoteThreshold <= 0 {
		return opts, errors.New("option vote_threshold must be in (0,1]")
	}
	if value, ok := values["frame_fraction"]; ok {
		number, valid := jsonNumber(value)
		if !valid || json.Unmarshal([]byte(number), &opts.VideoFrameFraction) != nil || opts.VideoFrameFraction < 0 || opts.VideoFrameFraction > 1 {
			return opts, errors.New("option frame_fraction must be a number in [0,1]")
		}
		opts.VideoFrameFractionSet = true
	}
	if err := readFloat("audio_tempo", &opts.AudioTempo, 0.5, 2); err != nil {
		return opts, err
	}
	if err := readFloat("audio_pitch", &opts.AudioPitch, -1000, 1000); err != nil {
		return opts, err
	}
	if err := readInt("ffmpeg_timeout", &opts.FFmpegTimeoutSeconds, 1); err != nil {
		return opts, err
	}
	if err := readInt("markllm_timeout", &opts.MarkLLMTimeout, 1); err != nil {
		return opts, err
	}
	if err := readString("strategy", &opts.Strategy); err != nil {
		return opts, err
	}
	if err := readString("backend", &opts.RewriteBackend); err != nil {
		return opts, err
	}
	if err := readString("model", &opts.RewriteModel); err != nil {
		return opts, err
	}
	if err := readString("base_url", &opts.RewriteBaseURL); err != nil {
		return opts, err
	}
	if err := readString("reasoning_effort", &opts.RewriteReasoningEffort); err != nil {
		return opts, err
	}
	if opts.RewriteReasoningEffort != "" {
		switch strings.ToLower(opts.RewriteReasoningEffort) {
		case "none", "low", "medium", "high", "off":
			opts.RewriteReasoningEffort = strings.ToLower(opts.RewriteReasoningEffort)
		default:
			return opts, fmt.Errorf("option reasoning_effort must be none, low, medium, high, or off")
		}
	}
	if err := readString("style", &opts.RewriteStyle); err != nil {
		return opts, err
	}
	if _, ok := values["strategy"]; ok {
		if _, err := ParseLayerBStrategy(opts.Strategy); err != nil {
			return opts, fmt.Errorf("option strategy: %w", err)
		}
	}
	if value, ok := values["temperature"]; ok {
		number, valid := jsonNumber(value)
		if !valid || json.Unmarshal([]byte(number), &opts.RewriteTemperature) != nil || opts.RewriteTemperature < 0 || opts.RewriteTemperature > 2 {
			return opts, fmt.Errorf("option temperature must be a number in [0,2]")
		}
	}
	if value, ok := values["timeout"]; ok {
		number, valid := jsonNumber(value)
		if !valid || json.Unmarshal([]byte(number), &opts.RewriteTimeoutSeconds) != nil || opts.RewriteTimeoutSeconds <= 0 || opts.RewriteTimeoutSeconds > 1800 {
			return opts, fmt.Errorf("option timeout must be an integer in [1,1800]")
		}
	}
	return opts, nil
}

func reportSuspicious(report FileReport) bool {
	if report.Text != nil && StylometrySuspicious(report.Text.Stylometry) {
		return true
	}
	if report.HasC2PA || report.HasAIMetadata || report.SuspiciousTotal > 0 {
		return true
	}
	for i, finding := range report.Findings {
		if i >= len(report.FindingsConfidence) || report.FindingsConfidence[i] != "informational" {
			if finding != "" && !strings.HasPrefix(strings.ToLower(finding), "no ") {
				return true
			}
		}
	}
	return false
}

func suspiciousEvidence(report FileReport, detections []any, threshold float64) map[string]any {
	stylometry := map[string]any{}
	if report.Text != nil && report.Text.Stylometry != nil {
		stylometry = report.Text.Stylometry
	}
	styleScore, _ := stylometry["score"].(float64)
	if threshold <= 0 {
		threshold = 0.65
	}
	stylePresent := stylometry["status"] == "ok" && styleScore >= threshold
	provenancePresent := report.HasC2PA || report.HasAIMetadata
	layerAPresent := report.SuspiciousTotal > 0
	detectorPresent := detectionsSuspicious(detections)
	if synthidIsWatermarked(report.SynthID) {
		detectorPresent = true
	}
	classes := map[string]any{
		"provenance": map[string]any{
			"present": provenancePresent, "strength": "definitive",
			"description": "Observable C2PA or AI-like provenance metadata embedded in the file.",
			"signals":     map[string]any{"has_c2pa": report.HasC2PA, "has_ai_metadata": report.HasAIMetadata},
		},
		"layer_a_unicode": map[string]any{
			"present": layerAPresent, "strength": "deterministic",
			"description": "Invisible or format Unicode carriers found in a text body.",
			"signals":     map[string]any{"suspicious_total": report.SuspiciousTotal},
		},
		"watermark_detector": map[string]any{
			"present": detectorPresent, "strength": "scheme_specific",
			"description": "A configured detector reported a positive result for its specific watermark scheme.",
			"signals":     map[string]any{"detected_any": detectorPresent, "detectors": detections},
		},
		"stylometry": map[string]any{
			"present": stylePresent, "strength": "heuristic",
			"description": "A heuristic stylometric score reached its configured threshold.",
			"signals":     map[string]any{"score": stylometry["score"], "density_tier": stylometry["density_tier"]},
		},
	}
	return map[string]any{
		"verdict":     provenancePresent || layerAPresent || detectorPresent || stylePresent,
		"description": "Heterogeneous evidence for follow-up inspection, not a single provenance or authorship judgment.",
		"classes":     classes,
	}
}

func detectReport(data []byte, report FileReport, opts Options) []any {
	detections := []any{}
	switch report.Kind {
	case KindText:
		stylometry := map[string]any{}
		if report.Text != nil && report.Text.Stylometry != nil {
			for key, value := range report.Text.Stylometry {
				stylometry[key] = value
			}
		} else {
			stylometry = scoreStylometry(string(data), report.Path, opts.Threshold)
		}
		stylometry["detector"] = "stylometry"
		stylometry["available"] = true
		// Keep the configured detector registry in the same order as the
		// upstream server: MarkLLM, keyed-Gumbel, Claude. Stylometry is a
		// separate heuristic section upstream, so append it after that
		// registry while retaining it in the richer Go response.
		detections = append(detections, runMarkLLMExternal(string(data), opts))
		if key := strings.TrimSpace(os.Getenv("WATERMARKS_GUMBEL_KEY")); key != "" {
			gumbel, err := DetectGumbelText(string(data), key, DefaultGumbelWindow, DefaultGumbelThreshold)
			if err != nil {
				detections = append(detections, map[string]any{"detector": "gumbel", "available": false, "error": err.Error()})
			} else {
				detections = append(detections, gumbel)
			}
		} else {
			detections = append(detections, map[string]any{
				"detector": "gumbel", "available": false,
				"error": "no same-key detector configured (set WATERMARKS_GUMBEL_KEY)",
			})
		}
		detections = append(detections,
			map[string]any{
				"detector": "claude-text", "available": false,
				"error": "no public Claude text detector endpoint configured",
			},
			stylometry,
		)
	case KindImage:
		detections = append(detections, map[string]any{
			"detector": "metadata", "available": true,
			"has_c2pa": report.HasC2PA, "has_ai_metadata": report.HasAIMetadata,
			"findings": report.Findings,
		})
		if report.SynthID != nil {
			detections = append(detections, report.SynthID)
		} else {
			detections = append(detections, map[string]any{
				"detector": "synthid", "available": false,
				"error": synthIDConfigurationError(opts),
			})
		}
	case KindAV, KindContainer:
		detections = append(detections, map[string]any{
			"detector": "metadata", "available": true,
			"has_c2pa": report.HasC2PA, "has_ai_metadata": report.HasAIMetadata,
			"findings": report.Findings,
		})
	}
	return detections
}

func synthIDConfigurationError(opts Options) string {
	if strings.TrimSpace(opts.SynthIDDir) != "" || strings.TrimSpace(os.Getenv("REVERSE_SYNTHID_DIR")) != "" {
		return "SynthID scorer unavailable (external score_synthid.py adapter did not return a verdict)"
	}
	return "no SynthID scorer configured (set WATERMARKS_SYNTHID_SCORER_URL or pass --synthid-dir with --upstream-scripts)"
}

// DetectBytes returns the ordinary file report together with every configured
// detector result used by the HTTP /detect endpoint. Keeping this in core
// makes the CLI and API use the same detector registry and fail-soft behavior.
func DetectBytes(data []byte, path string, opts Options) (FileReport, []any, error) {
	report, err := InspectBytes(data, path, opts)
	if err != nil {
		return FileReport{}, nil, err
	}
	return report, detectReport(data, report, opts), nil
}

// DetectFile is the file-oriented counterpart to DetectBytes.
func DetectFile(path string, opts Options) (FileReport, []any, error) {
	data, _, err := readRegular(path)
	if err != nil {
		return FileReport{}, nil, err
	}
	return DetectBytes(data, path, opts)
}

// DetectionsSuspicious reports whether a scheme-specific detector produced a
// positive verdict.
func DetectionsSuspicious(detections []any) bool {
	return detectionsSuspicious(detections)
}

func detectionsSuspicious(detections []any) bool {
	for _, item := range detections {
		values, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"is_watermarked", "suspicious", "verdict"} {
			if value, ok := values[key].(bool); ok && value {
				return true
			}
		}
	}
	return false
}

func writeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
