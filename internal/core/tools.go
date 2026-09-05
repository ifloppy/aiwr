package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const optionalToolTimeout = 30 * time.Second

var toolVersionArgs = map[string][]string{
	"c2patool": {"--version"},
	"exiftool": {"-ver"},
	"qpdf":     {"--version"},
	"ffmpeg":   {"-version"},
	"ffprobe":  {"-version"},
	"gs":       {"--version"},
}

var interestingExifLineRE = regexp.MustCompile(`(?i)c2pa|content.?credential|aigc|digital.?source|xmp|exif|iptc|jumb`)

// inspectOptionalTools runs the same best-effort external probes as the
// upstream image inspector. It never treats a missing or crashed optional
// program as a clean verdict; the result is retained in the report so callers
// can distinguish "not installed" from "answered no".
func inspectOptionalTools(path string, data []byte) map[string]any {
	return inspectOptionalToolsWithOptions(path, data, true)
}

func inspectOptionalToolsWithOptions(path string, data []byte, includeExiftool bool) map[string]any {
	probePath, cleanup, err := materializeProbeInput(path, data)
	if err != nil {
		return map[string]any{
			"c2patool": map[string]any{"available": false, "error": err.Error()},
			"exiftool": map[string]any{"available": false, "error": err.Error()},
		}
	}
	defer cleanup()

	tools := map[string]any{
		"c2patool": probeC2PATool(probePath),
		"exiftool": map[string]any{"available": false},
	}
	if includeExiftool {
		tools["exiftool"] = probeExiftool(probePath)
	}
	return tools
}

func materializeProbeInput(name string, data []byte) (string, func(), error) {
	if name != "" {
		if st, err := os.Lstat(name); err == nil && st.Mode().IsRegular() {
			return name, func() {}, nil
		}
	}
	ext := filepath.Ext(name)
	if len(ext) > 12 || strings.ContainsAny(ext, "/\\") {
		ext = ""
	}
	file, err := os.CreateTemp("", "aiwr-probe-*"+ext)
	if err != nil {
		return "", func() {}, err
	}
	temp := file.Name()
	cleanup := func() { _ = os.Remove(temp) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return temp, cleanup, nil
}

// imageExiftoolPass mirrors the optional second pass used by the upstream
// image cleaner. The core strip routines operate on bytes so they can also
// clean embedded images inside archives; for a top-level image, give
// exiftool a private extension-preserving file and carry its result back into
// the byte pipeline. This keeps CleanBytes and the HTTP service consistent
// with CleanFile without exposing the caller's path to an external command.
func imageExiftoolPass(data []byte, name string, opts Options) ([]byte, []string) {
	if opts.DisableExternalTools || !stripAllMetadata(opts) {
		return data, nil
	}
	if _, err := exec.LookPath("exiftool"); err != nil {
		return data, nil
	}
	ext := filepath.Ext(name)
	if len(ext) > 12 || strings.ContainsAny(ext, "/\\") {
		ext = ""
	}
	file, err := os.CreateTemp("", "aiwr-image-exiftool-*"+ext)
	if err != nil {
		return data, []string{"warning: exiftool image strip setup failed: " + err.Error()}
	}
	temp := file.Name()
	cleanup := func() { _ = os.Remove(temp) }
	defer cleanup()
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return data, []string{"warning: exiftool image strip setup failed: " + err.Error()}
	}
	stdout, stderr, returnCode, runErr := runOptionalTool(
		"exiftool", "-all=", "-overwrite_original", temp,
	)
	cleaned, readErr := os.ReadFile(temp)
	if readErr != nil {
		return data, []string{"warning: exiftool image strip produced no readable output: " + readErr.Error()}
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = runErr.Error()
		}
		return cleaned, []string{fmt.Sprintf("warning: exiftool image strip failed (exit %d): %s", returnCode, truncate(detail, 2000))}
	}
	return cleaned, []string{"exiftool -all= pass"}
}

func runOptionalTool(program string, args ...string) (stdout, stderr string, returnCode int, err error) {
	path, err := exec.LookPath(program)
	if err != nil {
		return "", "", 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), optionalToolTimeout)
	defer cancel()
	var out, diagnostic limitedBuffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	err = cmd.Run()
	if ctx.Err() != nil {
		return out.String(), diagnostic.String(), -1, fmt.Errorf("%s timed out after %s", program, optionalToolTimeout)
	}
	return out.String(), diagnostic.String(), cmd.ProcessState.ExitCode(), err
}

// commandUsable is deliberately stronger than LookPath for capability
// reporting. A binary can be present but fail before main (wrong
// architecture, missing loader, or an unusable installation). The exact
// probe is short and read-only.
func commandUsable(program string) bool {
	path, err := exec.LookPath(program)
	if err != nil {
		return false
	}
	args, ok := toolVersionArgs[program]
	if !ok {
		args = []string{"--version"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || ctx.Err() != nil {
		return false
	}
	return true
}

func probeC2PATool(path string) map[string]any {
	stdout, stderr, returnCode, err := runOptionalTool("c2patool", path)
	if err != nil {
		if _, lookErr := exec.LookPath("c2patool"); lookErr != nil {
			return map[string]any{"available": false}
		}
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = strings.TrimSpace(stdout)
		}
		if message == "" {
			message = err.Error()
		}
		// c2patool uses a non-zero exit status for an ordinary negative
		// answer.  Treat the documented no-claim/no-JUMBF messages as a
		// conclusive absence, while keeping crashes and other failures
		// inconclusive.  Exit status alone cannot distinguish those cases.
		lower := strings.ToLower(message)
		if strings.Contains(lower, "no claim") || strings.Contains(lower, "no jumbf") {
			return map[string]any{
				"available":    true,
				"ok":           true,
				"returncode":   returnCode,
				"has_manifest": false,
				"snippet":      truncate(message, 2000),
			}
		}
		return map[string]any{
			"available":    true,
			"ok":           false,
			"returncode":   returnCode,
			"has_manifest": false,
			"snippet":      truncate(message, 2000),
			"error":        truncate(fmt.Sprintf("exit %d, unrecognized output: %s", returnCode, message), 2000),
		}
	}
	output := stdout + stderr
	lower := strings.ToLower(output)
	noManifest := strings.Contains(lower, "no claim") || strings.Contains(lower, "no jumbf")
	hasManifest := !noManifest && (strings.Contains(lower, "claim") || strings.Contains(lower, "c2pa") || strings.Contains(lower, "manifest"))
	conclusive := noManifest || hasManifest
	entry := map[string]any{
		"available":    true,
		"returncode":   returnCode,
		"snippet":      truncate(output, 2000),
		"has_manifest": hasManifest,
		"ok":           conclusive,
	}
	if !conclusive {
		entry["error"] = fmt.Sprintf("exit %d, unrecognized output", returnCode)
	}
	return entry
}

func probeExiftool(path string) map[string]any {
	stdout, stderr, returnCode, err := runOptionalTool("exiftool", "-G1", "-a", "-s", path)
	if _, lookErr := exec.LookPath("exiftool"); lookErr != nil {
		return map[string]any{"available": false}
	}
	if err != nil && stdout == "" {
		stdout = stderr
	}
	lines := make([]string, 0, 50)
	for _, line := range strings.Split(stdout, "\n") {
		if interestingExifLineRE.MatchString(line) {
			lines = append(lines, line)
			if len(lines) == 50 {
				break
			}
		}
	}
	entry := map[string]any{"available": true, "interesting_lines": lines}
	if err != nil {
		entry["returncode"] = returnCode
		entry["error"] = truncate(strings.TrimSpace(stderr), 2000)
		if entry["error"] == "" {
			entry["error"] = truncate(err.Error(), 2000)
		}
	}
	return entry
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
