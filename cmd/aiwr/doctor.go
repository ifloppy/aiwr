package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ifloppy/aiwr/internal/core"
)

// doctorCheck is deliberately a small, stable JSON contract. External
// dependencies are optional for the native cleaner, so a missing check is
// reported without making the core unusable.
type doctorCheck struct {
	ID        string   `json:"id"`
	Category  string   `json:"category"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Optional  bool     `json:"optional"`
	Detail    string   `json:"detail"`
	Detected  string   `json:"detected,omitempty"`
	Configure []string `json:"configure,omitempty"`
}

type doctorSummary struct {
	Total   int `json:"total"`
	OK      int `json:"ok"`
	Missing int `json:"missing"`
	Warning int `json:"warning"`
	Error   int `json:"error"`
}

type doctorReport struct {
	OK       bool          `json:"ok"`
	Ready    bool          `json:"ready"`
	Complete bool          `json:"complete"`
	Version  string        `json:"version"`
	Summary  doctorSummary `json:"summary"`
	Checks   []doctorCheck `json:"checks"`
}

type doctorCommandSpec struct {
	ID        string
	Category  string
	Name      string
	Program   string
	Args      []string
	Purpose   string
	Configure string
}

func runDoctor(args []string) int {
	jsonOutput := false
	strict := false
	fs := newFlagSet("doctor")
	fs.BoolVar(&jsonOutput, "json", false, "emit machine-readable JSON")
	fs.BoolVar(&strict, "strict", false, "return 1 when optional checks are missing or unverified")
	if err := fs.Parse(normalizeFlagArgs(args)); err != nil {
		return cliError(err)
	}
	if fs.NArg() != 0 {
		return cliError(fmt.Errorf("doctor does not accept positional arguments"))
	}

	report := collectDoctorReport()
	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "aiwr: doctor JSON output failed: %s\n", err)
			return 1
		}
	} else {
		printDoctorReport(report)
	}
	if !report.OK || strict && !report.Complete {
		return 1
	}
	return 0
}

func collectDoctorReport() doctorReport {
	checks := []doctorCheck{{
		ID:       "core.native",
		Category: "core",
		Name:     "native Go cleaner",
		Status:   "ok",
		Detail:   "bundled and ready; no external runtime required",
	}}

	for _, spec := range []doctorCommandSpec{
		{ID: "tool.c2patool", Name: "c2patool", Program: "c2patool", Args: []string{"--version"}, Purpose: "C2PA manifest inspection", Configure: "Install c2patool and put it on PATH."},
		{ID: "tool.exiftool", Name: "ExifTool", Program: "exiftool", Args: []string{"-ver"}, Purpose: "metadata inspection and robust metadata cleaning", Configure: "Install ExifTool and put exiftool on PATH."},
		{ID: "tool.qpdf", Name: "qpdf", Program: "qpdf", Args: []string{"--version"}, Purpose: "structural PDF rewrite", Configure: "Install qpdf and put it on PATH."},
		{ID: "tool.ghostscript", Name: "Ghostscript", Program: "gs", Args: []string{"--version"}, Purpose: "PDF rendering fallback", Configure: "Install Ghostscript and put gs (or gswin64c on Windows) on PATH."},
		{ID: "tool.ffmpeg", Name: "ffmpeg", Program: "ffmpeg", Args: []string{"-version"}, Purpose: "audio remix and media processing", Configure: "Install ffmpeg and put it on PATH."},
		{ID: "tool.ffprobe", Name: "ffprobe", Program: "ffprobe", Args: []string{"-version"}, Purpose: "media stream probing", Configure: "Install ffprobe and put it on PATH."},
	} {
		checks = append(checks, doctorCommandCheck(spec))
	}

	checks = append(checks, doctorPythonCheck(), doctorCommandCheck(doctorCommandSpec{
		ID:        "runtime.node",
		Category:  "runtime",
		Name:      "Node.js",
		Program:   "node",
		Args:      []string{"--version"},
		Purpose:   "Claude Code, Grok, and generic command hooks",
		Configure: "Install Node.js and put node on PATH when agent hooks are needed.",
	}), doctorAdapterScriptsCheck())
	checks = append(checks,
		doctorURLCheck("backend.synthid-http", "SynthID image scorer", "WATERMARKS_SYNTHID_SCORER_URL", "HTTP scorer for pixel-domain detection", "Set WATERMARKS_SYNTHID_SCORER_URL to the scorer endpoint."),
		doctorURLCheck("backend.synthid-text", "SynthID text generator", "WATERMARKS_SYNTHID_TEXT_URL", "text watermark sidecar", "Set WATERMARKS_SYNTHID_TEXT_URL to the sidecar endpoint."),
		doctorDirectoryCheck("backend.markllm", "MarkLLM", "MARKLLM_DIR", "text watermark detection/generation", "Install and configure MarkLLM, then set MARKLLM_DIR."),
		doctorDirectoryCheck("backend.ctrlregen", "CtrlRegen", "NOAI_WATERMARK_DIR", "optional image/video pixel purification", "Install and configure CtrlRegen, then set NOAI_WATERMARK_DIR."),
		doctorDirectoryCheck("backend.markdiffusion", "MarkDiffusion", "MARKDIFFUSION_DIR", "optional image pixel purification", "Install and configure MarkDiffusion, then set MARKDIFFUSION_DIR."),
		doctorPresenceCheck("detector.gumbel", "same-key Gumbel detector", "WATERMARKS_GUMBEL_KEY", "same-key text watermark verification", "Set WATERMARKS_GUMBEL_KEY when the matching generator key is available."),
		doctorLayerBCheck(),
	)

	var summary doctorSummary
	for _, check := range checks {
		summary.Total++
		switch check.Status {
		case "ok":
			summary.OK++
		case "missing":
			summary.Missing++
		case "warning":
			summary.Warning++
		case "error":
			summary.Error++
		}
	}
	return doctorReport{
		OK:       summary.Error == 0,
		Ready:    true,
		Complete: summary.Missing == 0 && summary.Warning == 0 && summary.Error == 0,
		Version:  core.Version,
		Summary:  summary,
		Checks:   checks,
	}
}

func doctorCommandCheck(spec doctorCommandSpec) doctorCheck {
	category := spec.Category
	if category == "" {
		category = "tool"
	}
	check := doctorCheck{
		ID:       spec.ID,
		Category: category,
		Name:     spec.Name,
		Optional: true,
	}
	path, err := exec.LookPath(spec.Program)
	if err != nil && spec.ID == "tool.ghostscript" && runtime.GOOS == "windows" {
		for _, alias := range []string{"gswin64c", "gswin32c"} {
			if candidate, aliasErr := exec.LookPath(alias); aliasErr == nil {
				path, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("not found; %s unavailable", spec.Purpose)
		check.Configure = []string{spec.Configure}
		return check
	}
	output, probeErr := doctorRun(path, spec.Args...)
	if probeErr != nil {
		check.Status = "error"
		check.Detail = fmt.Sprintf("found but version probe failed; %s: %s", spec.Purpose, doctorErrorDetail(output, probeErr))
		check.Configure = []string{spec.Configure, fmt.Sprintf("Repair the %s installation and retry aiwr doctor.", spec.Name)}
		return check
	}
	check.Status = "ok"
	check.Detected = path
	check.Detail = fmt.Sprintf("available for %s", spec.Purpose)
	return check
}

func doctorPythonCheck() doctorCheck {
	check := doctorCheck{
		ID:       "runtime.python",
		Category: "runtime",
		Name:     "Python 3.10+",
		Optional: true,
	}
	type candidate struct {
		program string
		args    []string
	}
	candidates := []candidate{}
	for _, name := range []string{"AIWR_PYTHON", "WATERMARKS_PYTHON"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			candidates = append(candidates, candidate{program: value})
		}
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, candidate{program: "py", args: []string{"-3"}})
	}
	candidates = append(candidates, candidate{program: "python3"}, candidate{program: "python"})
	found := []string{}
	for _, item := range candidates {
		path, err := exec.LookPath(item.program)
		if err != nil {
			continue
		}
		found = append(found, path)
		args := append([]string{}, item.args...)
		args = append(args, "-c", "import sys; print('.'.join(str(x) for x in sys.version_info[:3])); sys.exit(0 if sys.version_info >= (3, 10) else 1)")
		output, probeErr := doctorRun(path, args...)
		if probeErr == nil {
			check.Status = "ok"
			check.Detected = path
			check.Detail = fmt.Sprintf("available for optional Python/GPU adapters (%s)", doctorFirstLine(output))
			return check
		}
	}
	if len(found) == 0 {
		check.Status = "missing"
		check.Detail = "not found; optional Python/GPU adapters cannot run"
		check.Configure = []string{"Install Python 3.10+ only if you use the optional upstream/ML adapters."}
	} else {
		check.Status = "error"
		check.Detail = "Python was found, but no Python 3.10+ interpreter passed the probe"
		check.Configure = []string{
			"Select a Python 3.10+ interpreter with AIWR_PYTHON or WATERMARKS_PYTHON.",
		}
	}
	return check
}

func doctorAdapterScriptsCheck() doctorCheck {
	check := doctorCheck{
		ID:       "adapter.scripts",
		Category: "adapter",
		Name:     "aiwr adapter scripts",
		Optional: true,
	}
	dir := doctorAdapterScriptsDir()
	if dir == "" {
		check.Status = "missing"
		check.Detail = "not configured; optional research adapters are unavailable"
		check.Configure = []string{
			"Set AIWR_UPSTREAM_SCRIPTS to the directory containing the optional adapter scripts (usually /path/to/aiwr/service/scripts).",
		}
		return check
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		check.Status = "error"
		check.Detail = fmt.Sprintf("configured directory is not usable: %s", dir)
		check.Configure = []string{"Correct AIWR_UPSTREAM_SCRIPTS and retry aiwr doctor."}
		return check
	}
	missing := []string{}
	for _, name := range []string{
		"hook_written_file.py",
		"score_synthid.py",
		"synthid_score_server.py",
		"synthid_text_server.py",
		"detect_text_watermark.py",
		"rewrite_text.py",
		"clean_ctrlregen.py",
		"markdiffusion_harness.py",
	} {
		path := filepath.Join(dir, name)
		if st, statErr := os.Stat(path); statErr != nil || !st.Mode().IsRegular() {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		check.Status = "warning"
		check.Detail = fmt.Sprintf("directory found, but optional scripts are missing: %s", strings.Join(missing, ", "))
		check.Configure = []string{"Install or check out the upstream adapter scripts, then retry aiwr doctor."}
		return check
	}
	check.Status = "ok"
	check.Detected = dir
	check.Detail = "optional adapter entrypoints found; third-party runtimes are checked separately"
	return check
}

func doctorAdapterScriptsDir() string {
	for _, value := range []string{
		os.Getenv("AIWR_UPSTREAM_SCRIPTS"),
		os.Getenv("WATERMARKS_UPSTREAM_SCRIPTS"),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return filepath.Clean(value)
		}
	}
	if value := strings.TrimSpace(os.Getenv("WATERMARKS_UPSTREAM_REPO")); value != "" {
		return filepath.Join(filepath.Clean(value), "service", "scripts")
	}
	for _, candidate := range []string{filepath.Join("service", "scripts")} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr == nil {
				return absolute
			}
		}
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "service", "scripts")
		if st, statErr := os.Stat(candidate); statErr == nil && st.IsDir() {
			return candidate
		}
	}
	return ""
}

func doctorURLCheck(id, name, env, purpose, configure string) doctorCheck {
	check := doctorCheck{ID: id, Category: "backend", Name: name, Optional: true}
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("%s is not configured; %s remains unavailable", env, purpose)
		check.Configure = []string{configure}
		return check
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		check.Status = "error"
		check.Detail = fmt.Sprintf("%s is set but is not a valid HTTP(S) URL without userinfo", env)
		check.Configure = []string{fmt.Sprintf("Correct %s and retry aiwr doctor.", env)}
		return check
	}
	check.Status = "warning"
	check.Detail = fmt.Sprintf("configured for %s; endpoint is not contacted by the offline doctor", purpose)
	check.Configure = []string{"After authorizing network access, verify the endpoint health manually."}
	return check
}

func doctorDirectoryCheck(id, name, env, purpose, configure string) doctorCheck {
	check := doctorCheck{ID: id, Category: "backend", Name: name, Optional: true}
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("%s is not set; %s is unavailable", env, purpose)
		check.Configure = []string{configure}
		return check
	}
	path := filepath.Clean(raw)
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		check.Status = "error"
		check.Detail = fmt.Sprintf("%s points to a missing or non-directory path", env)
		check.Configure = []string{fmt.Sprintf("Correct %s and retry aiwr doctor.", env)}
		return check
	}
	check.Status = "warning"
	check.Detected = path
	check.Detail = fmt.Sprintf("checkout directory is present for %s; package/model loading is not attempted", purpose)
	check.Configure = append(check.Configure, fmt.Sprintf("Verify the %s runtime/model manually, then rerun aiwr doctor.", name))
	return check
}

func doctorPresenceCheck(id, name, env, purpose, configure string) doctorCheck {
	check := doctorCheck{ID: id, Category: "backend", Name: name, Optional: true}
	if strings.TrimSpace(os.Getenv(env)) == "" {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("%s is not configured; %s is unavailable", env, purpose)
		check.Configure = []string{configure}
		return check
	}
	check.Status = "ok"
	check.Detail = fmt.Sprintf("key is configured for %s", purpose)
	return check
}

func doctorLayerBCheck() doctorCheck {
	check := doctorCheck{ID: "backend.layerb", Category: "backend", Name: "Layer B rewrite", Optional: true}
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_BACKEND")))
	if backend == "" {
		check.Status = "missing"
		check.Detail = "not configured; Layer A remains fully available"
		check.Configure = []string{
			"For local Ollama: set WATERMARKS_REWRITE_BACKEND=ollama, WATERMARKS_REWRITE_MODEL, and WATERMARKS_REWRITE_BASE_URL.",
			"For an OpenAI-compatible endpoint: also set WATERMARKS_REWRITE_API_KEY or OPENAI_API_KEY.",
		}
		return check
	}
	if backend == "openai" || backend == "openai_compatible" {
		backend = "openai-compatible"
	}
	if backend == "print-prompt" {
		check.Status = "ok"
		check.Detail = "prompt-only mode configured; no model endpoint is required"
		return check
	}
	if backend != "ollama" && backend != "openai-compatible" {
		check.Status = "error"
		check.Detail = fmt.Sprintf("unsupported WATERMARKS_REWRITE_BACKEND value %q", backend)
		check.Configure = []string{"Use ollama, openai-compatible, or print-prompt."}
		return check
	}
	missing := []string{}
	if strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_MODEL")) == "" {
		missing = append(missing, "WATERMARKS_REWRITE_MODEL")
	}
	baseURL := strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_BASE_URL"))
	if baseURL == "" {
		missing = append(missing, "WATERMARKS_REWRITE_BASE_URL")
	} else if err := doctorRewriteURL(baseURL); err != nil {
		check.Status = "error"
		check.Detail = err.Error()
		check.Configure = []string{"Set WATERMARKS_REWRITE_BASE_URL to a valid HTTP(S) endpoint."}
		return check
	}
	if backend == "openai-compatible" && strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_API_KEY")) == "" && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		missing = append(missing, "WATERMARKS_REWRITE_API_KEY or OPENAI_API_KEY")
	}
	if len(missing) > 0 {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("backend %s is selected but required configuration is missing: %s", backend, strings.Join(missing, ", "))
		check.Configure = []string{"Set the missing variables listed in the detail, then rerun aiwr doctor."}
		return check
	}
	if !doctorRewriteEndpointIsLocal(baseURL) && !doctorEnvTrue("WATERMARKS_REWRITE_ALLOW_REMOTE") {
		check.Status = "error"
		check.Detail = "remote rewrite endpoint requires WATERMARKS_REWRITE_ALLOW_REMOTE=1"
		check.Configure = []string{"Review the endpoint and set WATERMARKS_REWRITE_ALLOW_REMOTE=1 only after authorizing remote text transfer."}
		return check
	}
	check.Status = "warning"
	check.Detail = fmt.Sprintf("backend %s, model, and endpoint are configured; model endpoint is not contacted by the offline doctor", backend)
	return check
}

func doctorRewriteURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("WATERMARKS_REWRITE_BASE_URL must be a valid HTTP(S) URL without userinfo")
	}
	return nil
}

func doctorRewriteEndpointIsLocal(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func doctorEnvTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func doctorRun(program string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stderr.String(), ctx.Err()
	}
	if err != nil {
		output := strings.TrimSpace(stdout.String())
		if output == "" {
			output = strings.TrimSpace(stderr.String())
		}
		return output, err
	}
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}
	return output, nil
}

func doctorFirstLine(value string) string {
	line := strings.TrimSpace(strings.SplitN(value, "\n", 2)[0])
	if len(line) > 160 {
		return line[:160] + "…"
	}
	return line
}

func doctorErrorDetail(output string, err error) string {
	if line := doctorFirstLine(output); line != "" {
		return line
	}
	return err.Error()
}

func printDoctorReport(report doctorReport) {
	fmt.Println(cliText("aiwr doctor", "aiwr doctor"))
	for _, check := range report.Checks {
		glyph := "?"
		status := check.Status
		switch check.Status {
		case "ok":
			glyph = "✓"
			status = cliText("ok", "通过")
		case "missing":
			glyph = "-"
			status = cliText("not configured", "未配置")
		case "warning":
			glyph = "!"
			status = cliText("warning", "待验证")
		case "error":
			glyph = "×"
			status = cliText("error", "错误")
		}
		fmt.Fprintf(os.Stdout, "%s %-24s %-14s %s\n", glyph, check.ID, status, check.Detail)
		for _, instruction := range check.Configure {
			fmt.Fprintf(os.Stdout, "  -> %s\n", instruction)
		}
	}
	fmt.Fprintf(os.Stdout, "\n%s\n", cliText(
		fmt.Sprintf("summary: %d ok, %d missing, %d warning, %d error", report.Summary.OK, report.Summary.Missing, report.Summary.Warning, report.Summary.Error),
		fmt.Sprintf("摘要：%d 通过，%d 未配置，%d 待验证，%d 错误", report.Summary.OK, report.Summary.Missing, report.Summary.Warning, report.Summary.Error),
	))
	if report.OK {
		fmt.Println(cliText("native core is ready; optional gaps do not block basic cleaning", "原生核心已就绪；可选项缺失不影响基础清理"))
	} else {
		fmt.Println(cliText("configured errors need attention; basic cleaning may still work", "存在已配置但错误的项目，请处理；基础清理可能仍可用"))
	}
}
