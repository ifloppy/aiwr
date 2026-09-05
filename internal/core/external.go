package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The research/GPU implementations are intentionally runtime adapters. The
// Go distribution remains small and MIT-only, while users who have accepted
// the corresponding upstream licenses can point aiwr at a checkout.
func externalScriptsDir(opts Options) string {
	for _, value := range []string{
		opts.UpstreamScriptsDir,
		os.Getenv("AIWR_UPSTREAM_SCRIPTS"),
		os.Getenv("WATERMARKS_UPSTREAM_SCRIPTS"),
	} {
		if dir := strings.TrimSpace(value); dir != "" {
			return filepath.Clean(dir)
		}
	}
	if repo := strings.TrimSpace(os.Getenv("WATERMARKS_UPSTREAM_REPO")); repo != "" {
		return filepath.Join(filepath.Clean(repo), "service", "scripts")
	}
	// Keep the optional upstream command surface usable from a source checkout
	// without requiring callers to repeat the repository path.  Installed
	// binaries still use an explicit checkout when the bundled scripts are not
	// alongside the executable.
	for _, candidate := range bundledScriptsCandidates() {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	return ""
}

func bundledScriptsCandidates() []string {
	candidates := []string{filepath.Join("service", "scripts")}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(dir, "service", "scripts"),
			filepath.Join(dir, "..", "service", "scripts"),
		)
	}
	if working, err := os.Getwd(); err == nil {
		current := working
		for i := 0; i < 4; i++ {
			candidates = append(candidates, filepath.Join(current, "service", "scripts"))
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	return candidates
}

func externalScript(name string, opts Options) (string, string, error) {
	if name == "" || filepath.Base(name) != name || filepath.Ext(name) != ".py" {
		return "", "", fmt.Errorf("invalid external adapter script name %q", name)
	}
	dir := externalScriptsDir(opts)
	if dir == "" {
		return "", "", fmt.Errorf("%s adapter is not configured; use the bundled service/scripts or pass --upstream-scripts", name)
	}
	candidates := []string{dir}
	// Accept either the upstream service/scripts directory or the repository
	// root, which is convenient when users clone the reference repository.
	candidates = append(candidates, filepath.Join(dir, "service", "scripts"), filepath.Join(dir, "scripts"))
	for _, candidate := range candidates {
		script := filepath.Join(candidate, name)
		if st, err := os.Stat(script); err == nil && st.Mode().IsRegular() {
			absoluteScript, absErr := filepath.Abs(script)
			if absErr != nil {
				return "", "", absErr
			}
			absoluteDir, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", "", absErr
			}
			return absoluteScript, absoluteDir, nil
		}
	}
	return "", dir, fmt.Errorf("upstream adapter script %s not found below %s", name, dir)
}

func externalPython(scriptsDir string) string {
	for _, value := range []string{
		os.Getenv("AIWR_PYTHON"),
		os.Getenv("WATERMARKS_PYTHON"),
	} {
		if value = strings.TrimSpace(value); value != "" {
			if path, err := exec.LookPath(value); err == nil {
				return path
			}
		}
	}
	repo := filepath.Dir(filepath.Dir(scriptsDir))
	for _, candidate := range []string{
		filepath.Join(repo, ".venv", "bin", "python"),
		filepath.Join(repo, ".venv", "Scripts", "python.exe"),
	} {
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate
		}
	}
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func runExternalJSON(scriptName string, args []string, opts Options, timeout int, env map[string]string) (map[string]any, error) {
	script, scriptsDir, err := externalScript(scriptName, opts)
	if err != nil {
		return map[string]any{"available": false, "error": err.Error()}, nil
	}
	python := externalPython(scriptsDir)
	if python == "" {
		return map[string]any{"available": false, "error": "python3 is not available for the external adapter"}, nil
	}
	if timeout <= 0 {
		timeout = 3600
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	commandArgs := append([]string{script}, args...)
	cmd := exec.CommandContext(ctx, python, commandArgs...)
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(env) > 0 {
		cmd.Env = environmentWithOverrides(env)
	}
	err = cmd.Run()
	if ctx.Err() != nil {
		return map[string]any{"available": false, "error": fmt.Sprintf("%s timed out after %ds", scriptName, timeout)}, nil
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return map[string]any{"available": false, "error": truncate(message, 2000)}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return map[string]any{"available": false, "error": fmt.Sprintf("bad %s adapter JSON: %v", scriptName, err)}, nil
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["available"] = true
	return payload, nil
}

// RunExternalCLI executes one of the optional upstream command-line tools and
// streams its standard streams unchanged.  The Go CLI uses this for commands
// whose upstream behavior is intentionally kept in the user's own checkout;
// no Python or research/GPU dependency is imported into the Go binary.
func RunExternalCLI(scriptName string, args []string, opts Options) (int, error) {
	script, scriptsDir, err := externalScript(scriptName, opts)
	if err != nil {
		return 0, err
	}
	python := externalPython(scriptsDir)
	if python == "" {
		return 0, errors.New("python3 is not available for the external adapter")
	}
	commandArgs := append([]string{script}, args...)
	cmd := exec.Command(python, commandArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

func runExternalRewrite(text, strategy string, opts Options) (string, map[string]any, error) {
	script, scriptsDir, err := externalScript("rewrite_text.py", opts)
	if err != nil {
		return "", nil, err
	}
	python := externalPython(scriptsDir)
	if python == "" {
		return "", nil, errors.New("python3 is not available for the Layer B external adapter")
	}
	tempDir, err := os.MkdirTemp("", "aiwr-rewrite-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tempDir)
	input := filepath.Join(tempDir, "input.txt")
	output := filepath.Join(tempDir, "output.txt")
	if err := os.WriteFile(input, []byte(text), 0o600); err != nil {
		return "", nil, err
	}
	backend := strings.TrimSpace(opts.RewriteBackend)
	if backend == "" {
		backend = strings.TrimSpace(os.Getenv("WATERMARKS_REWRITE_BACKEND"))
	}
	if backend == "" {
		backend = "print-prompt"
	}
	args := []string{input, "-o", output, "--strategy", strategy, "--backend", backend,
		"--timeout", strconv.Itoa(maxInt(opts.RewriteTimeoutSeconds, 1)),
		"--temperature", strconv.FormatFloat(opts.RewriteTemperature, 'g', -1, 64),
		"--no-layer-a-after", "--json-stats"}
	if effort := strings.TrimSpace(opts.RewriteReasoningEffort); effort != "" {
		args = append(args, "--reasoning-effort", effort)
	}
	if opts.RewriteTargetMargin > 0 {
		args = append(args, "--target-margin", strconv.FormatFloat(opts.RewriteTargetMargin, 'g', -1, 64))
	}
	if opts.RewriteChunkShuffle {
		args = append(args, "--chunk-shuffle")
	}
	if opts.RewriteNoopLexFloor >= 0 {
		args = append(args, "--noop-lex-floor", strconv.FormatFloat(opts.RewriteNoopLexFloor, 'g', -1, 64))
	}
	if scheme := strings.TrimSpace(opts.MarkLLMScheme); scheme != "" {
		args = append(args, "--markllm-scheme", scheme)
	}
	if dir := strings.TrimSpace(opts.MarkLLMDir); dir != "" {
		args = append(args, "--markllm-dir", dir)
	}
	if model := strings.TrimSpace(opts.MarkLLMModel); model != "" {
		args = append(args, "--markllm-model", model)
	}
	if opts.MarkLLMTimeout > 0 {
		args = append(args, "--markllm-timeout", strconv.Itoa(opts.MarkLLMTimeout))
	}
	if model := strings.TrimSpace(opts.RewriteModel); model != "" {
		args = append(args, "--model", model)
	}
	if baseURL := strings.TrimSpace(opts.RewriteBaseURL); baseURL != "" {
		args = append(args, "--base-url", baseURL)
	}
	if opts.RewriteAllowRemote {
		args = append(args, "--allow-remote")
	}
	if style := strings.TrimSpace(opts.RewriteStyle); style != "" {
		args = append(args, "--style", style)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(maxInt(opts.RewriteTimeoutSeconds, 1))*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, append([]string{script}, args...)...)
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	overrides := map[string]string{}
	if key := strings.TrimSpace(opts.RewriteAPIKey); key != "" {
		overrides["WATERMARKS_REWRITE_API_KEY"] = key
	}
	if key := strings.TrimSpace(opts.RewriteGumbelKey); key != "" {
		overrides["WATERMARKS_GUMBEL_KEY"] = key
	}
	if dir := strings.TrimSpace(opts.MarkLLMDir); dir != "" {
		overrides["MARKLLM_DIR"] = dir
	}
	if scheme := strings.TrimSpace(opts.MarkLLMScheme); scheme != "" {
		overrides["WATERMARKS_MARKLLM_SCHEME"] = scheme
	}
	if model := strings.TrimSpace(opts.MarkLLMModel); model != "" {
		overrides["MARKLLM_MODEL"] = model
	}
	if opts.MarkLLMTimeout > 0 {
		overrides["WATERMARKS_MARKLLM_TIMEOUT"] = strconv.Itoa(opts.MarkLLMTimeout)
	}
	if len(overrides) > 0 {
		cmd.Env = environmentWithOverrides(overrides)
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("Layer B external rewrite timed out after %ds", maxInt(opts.RewriteTimeoutSeconds, 1))
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return "", nil, fmt.Errorf("Layer B external rewrite failed: %s", truncate(message, 2000))
	}
	cleaned, err := readRegularBytes(output)
	if err != nil {
		return "", nil, fmt.Errorf("Layer B external rewrite produced no output: %w", err)
	}
	stats := map[string]any{"backend": backend, "mode": "external", "strategy": strategy}
	if parsed := lastJSONMap(stderr.String()); parsed != nil {
		stats = parsed
	}
	return string(cleaned), stats, nil
}

func environmentWithOverrides(overrides map[string]string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func lastJSONMap(value string) map[string]any {
	var result map[string]any
	for start := 0; start < len(value); {
		offset := strings.IndexByte(value[start:], '{')
		if offset < 0 {
			break
		}
		start += offset
		var candidate map[string]any
		if json.Unmarshal([]byte(value[start:]), &candidate) == nil && candidate != nil {
			result = candidate
		}
		start++
	}
	return result
}

func maxInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func createExternalTemp(dir, base, ext string, data []byte, mode os.FileMode) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := ensureNoSymlinkComponents(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if len(ext) > 12 || strings.ContainsAny(ext, "/\\") {
		ext = ""
	}
	file, err := os.CreateTemp(dir, "."+base+"-*.aiwr"+ext)
	if err != nil {
		return "", err
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := file.Chmod(mode.Perm()); err != nil {
		_ = file.Close()
		cleanup()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", err
	}
	return name, nil
}

func externalOutputTemp(dir, base, ext string, mode os.FileMode) (string, error) {
	name, err := createExternalTemp(dir, base, ext, nil, mode)
	if err != nil {
		return "", err
	}
	return name, nil
}

func ctrlregenDirectory(opts Options) string {
	if value := strings.TrimSpace(opts.CtrlRegenDir); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("NOAI_WATERMARK_DIR"))
}

func markDiffusionDirectory(opts Options) string {
	if value := strings.TrimSpace(opts.MarkDiffusionDir); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("MARKDIFFUSION_DIR"))
}

func runCtrlRegen(input, output string, opts Options) (map[string]any, error) {
	dir := ctrlregenDirectory(opts)
	if dir == "" {
		return map[string]any{"available": false, "error": "CtrlRegen not configured (set NOAI_WATERMARK_DIR or pass --ctrlregen-dir)"}, nil
	}
	if opts.CtrlRegenIntensity <= 0 || opts.CtrlRegenIntensity > 1 {
		return nil, errors.New("CtrlRegen intensity must be in (0,1]")
	}
	if opts.CtrlRegenSteps < 1 {
		return nil, errors.New("CtrlRegen steps must be >= 1")
	}
	args := []string{
		input, "-o", output, "--upstream-dir", dir,
		"--intensity", strconv.FormatFloat(opts.CtrlRegenIntensity, 'g', -1, 64),
		"--steps", strconv.Itoa(opts.CtrlRegenSteps), "--json",
	}
	if device := strings.TrimSpace(opts.CtrlRegenDevice); device != "" {
		args = append(args, "--device", device)
	}
	if opts.CtrlRegenSeedSet {
		args = append(args, "--seed", strconv.Itoa(opts.CtrlRegenSeed))
	}
	return runExternalJSON("clean_ctrlregen.py", args, opts, opts.CtrlRegenTimeout, nil)
}

func runMarkDiffusion(input, output string, opts Options) (map[string]any, error) {
	dir := markDiffusionDirectory(opts)
	if dir == "" {
		return map[string]any{"available": false, "error": "MarkDiffusion not configured (set MARKDIFFUSION_DIR or pass --markdiffusion-dir)"}, nil
	}
	if opts.MarkDiffusionIntensity <= 0 || opts.MarkDiffusionIntensity > 1 {
		return nil, errors.New("MarkDiffusion intensity must be in (0,1]")
	}
	if opts.MarkDiffusionSize < 1 || opts.MarkDiffusionSteps < 1 {
		return nil, errors.New("MarkDiffusion size and steps must be >= 1")
	}
	args := []string{
		"purify", input, "-o", output, "--upstream-dir", dir,
		"--purification-intensity", strconv.FormatFloat(opts.MarkDiffusionIntensity, 'g', -1, 64),
		"--size", strconv.Itoa(opts.MarkDiffusionSize),
		"--steps", strconv.Itoa(opts.MarkDiffusionSteps), "--json",
	}
	if model := strings.TrimSpace(opts.MarkDiffusionModel); model != "" {
		args = append(args, "--model", model)
	}
	if device := strings.TrimSpace(opts.MarkDiffusionDevice); device != "" {
		args = append(args, "--device", device)
	}
	return runExternalJSON("markdiffusion_harness.py", args, opts, opts.MarkDiffusionTimeout, nil)
}

func runVideoPurify(input, output string, opts Options) (map[string]any, error) {
	if opts.RemovePixel != "ctrlregen" && opts.RemovePixel != "diffusion" {
		return nil, errors.New("video pixel remover must be ctrlregen or diffusion")
	}
	if opts.VideoVoteThreshold <= 0 || opts.VideoVoteThreshold > 1 {
		return nil, errors.New("video vote threshold must be in (0,1]")
	}
	args := []string{
		input, "-o", output, "--remove-pixel", opts.RemovePixel,
		"--vote-threshold", strconv.FormatFloat(opts.VideoVoteThreshold, 'g', -1, 64),
		"--json",
	}
	if opts.VideoFrameFractionSet {
		args = append(args, "--frame-fraction", strconv.FormatFloat(opts.VideoFrameFraction, 'g', -1, 64))
	}
	env := map[string]string{}
	if dir := ctrlregenDirectory(opts); dir != "" {
		env["NOAI_WATERMARK_DIR"] = dir
	}
	if dir := markDiffusionDirectory(opts); dir != "" {
		env["MARKDIFFUSION_DIR"] = dir
	}
	return runExternalJSON("clean_video.py", args, opts, opts.FFmpegTimeoutSeconds, env)
}

func runSynthIDExternal(data []byte, opts Options) map[string]any {
	dir := strings.TrimSpace(opts.SynthIDDir)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("REVERSE_SYNTHID_DIR"))
	}
	if dir == "" {
		return nil
	}
	temp, err := createExternalTemp("", "synthid", ".png", data, 0o600)
	if err != nil {
		return map[string]any{"detector": "synthid", "available": false, "error": err.Error()}
	}
	defer os.Remove(temp)
	// The temp file is deliberately passed through the same external script
	// path as the CLI; no reverse-SynthID code is imported into the Go process.
	payload, _ := runExternalJSON("score_synthid.py", []string{
		temp, "--upstream-dir", dir, "--json",
	}, opts, 180, nil)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["detector"] = "synthid"
	return payload
}

func runMarkLLMExternal(text string, opts Options) map[string]any {
	markllmDir := strings.TrimSpace(opts.MarkLLMDir)
	if markllmDir == "" {
		markllmDir = strings.TrimSpace(os.Getenv("MARKLLM_DIR"))
	}
	scheme := strings.TrimSpace(opts.MarkLLMScheme)
	if scheme == "" {
		scheme = strings.TrimSpace(os.Getenv("WATERMARKS_MARKLLM_SCHEME"))
	}
	if scheme == "" {
		scheme = "kgw"
	}
	report := map[string]any{
		"detector": "markllm", "scheme": scheme, "vendor": "open-llm", "available": false,
	}
	if markllmDir == "" {
		report["error"] = "MARKLLM_DIR not set"
		return report
	}
	temp, err := createExternalTemp("", "markllm", ".txt", []byte(text), 0o600)
	if err != nil {
		report["error"] = err.Error()
		return report
	}
	defer os.Remove(temp)
	args := []string{"detect", temp, "--upstream-dir", markllmDir, "--scheme", scheme, "--json"}
	model := strings.TrimSpace(opts.MarkLLMModel)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("MARKLLM_MODEL"))
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	timeout := opts.MarkLLMTimeout
	if timeout <= 0 {
		timeout = 600
	}
	if raw := strings.TrimSpace(os.Getenv("WATERMARKS_MARKLLM_TIMEOUT")); raw != "" {
		if opts.MarkLLMTimeout <= 0 {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
				timeout = parsed
			}
		}
	}
	payload, _ := runExternalJSON("detect_text_watermark.py", args, opts, timeout, nil)
	if payload == nil {
		return report
	}
	for key, value := range payload {
		report[key] = value
	}
	report["detector"] = "markllm"
	return report
}

func runPixelBackend(data []byte, inputName, dest string, mode os.FileMode, opts Options) ([]byte, map[string]any, error) {
	if opts.RemovePixel == "" {
		return data, nil, nil
	}
	if opts.RemovePixel != "ctrlregen" && opts.RemovePixel != "diffusion" {
		return nil, nil, errors.New("remove-pixel must be ctrlregen or diffusion")
	}
	dir := filepath.Dir(dest)
	ext := filepath.Ext(inputName)
	if ext == "" {
		ext = filepath.Ext(dest)
	}
	inputTemp, err := createExternalTemp(dir, filepath.Base(inputName), ext, data, mode)
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(inputTemp)
	outputTemp, err := externalOutputTemp(dir, filepath.Base(dest), ext, mode)
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(outputTemp)
	var report map[string]any
	if isVideoName(inputName) {
		report, err = runVideoPurify(inputTemp, outputTemp, opts)
	} else {
		switch opts.RemovePixel {
		case "ctrlregen":
			report, err = runCtrlRegen(inputTemp, outputTemp, opts)
		case "diffusion":
			report, err = runMarkDiffusion(inputTemp, outputTemp, opts)
		}
	}
	if err != nil {
		return nil, report, err
	}
	if report == nil || report["available"] != true {
		message := "pixel backend unavailable"
		if report != nil {
			if value, ok := report["error"].(string); ok && value != "" {
				message = value
			}
		}
		return nil, report, errors.New(message)
	}
	cleaned, err := readRegularBytes(outputTemp)
	if err != nil {
		return nil, report, fmt.Errorf("pixel backend produced no readable output: %w", err)
	}
	return cleaned, report, nil
}

func readRegularBytes(path string) ([]byte, error) {
	data, _, err := readRegular(path)
	return data, err
}
