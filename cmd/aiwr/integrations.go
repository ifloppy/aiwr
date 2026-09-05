package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/iruanp/aiwr/internal/core"
)

// check-staged and clean-staged intentionally live in the Go CLI. They are
// repository-integrator commands, not a second cleaning implementation: all
// format routing and mutation still goes through internal/core.
func runCheckStaged(args []string) int {
	checkStylometry := false
	fs := newFlagSet("check-staged")
	fs.BoolVar(&checkStylometry, "check-stylometry", false, "include heuristic text stylometry")
	if err := fs.Parse(normalizeFlagArgs(args)); err != nil {
		return cliError(err)
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return cliError(errors.New("check-staged requires at least one file"))
	}

	opts := core.DefaultOptions()
	opts.Stylometry = checkStylometry
	actionable := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			if err == nil {
				err = errors.New("not a regular file")
			}
			fmt.Fprintf(os.Stderr, "not a file: %s\n", path)
			return 2
		}
		if st.Size() > core.MaxInputBytes() {
			fmt.Fprintf(os.Stderr, "skipping %s: larger than %d bytes\n", path, core.MaxInputBytes())
			continue
		}
		report, inspectErr := core.InspectFile(path, opts)
		if inspectErr != nil {
			fmt.Fprintf(os.Stderr, "aiwr: %s: %v\n", path, inspectErr)
			return 2
		}
		if report.Kind == core.KindUnknown {
			continue
		}
		item := auditItem(report, opts)
		if auditItemActionable(item) {
			actionable = append(actionable, item)
		}
	}
	if len(actionable) == 0 {
		return 0
	}
	fmt.Fprintf(os.Stderr, "aiwr: %d file(s) carry AI/C2PA provenance marks:\n", len(actionable))
	for _, item := range actionable {
		fmt.Fprintf(os.Stderr, "  %v\n", item["path"])
		if findings, ok := item["findings"].([]string); ok {
			for _, finding := range findings {
				fmt.Fprintf(os.Stderr, "    - %s\n", finding)
			}
		}
		if value, _ := item["has_c2pa"].(bool); value {
			fmt.Fprintln(os.Stderr, "    - C2PA manifest present")
		}
		if value, _ := item["has_ai_metadata"].(bool); value {
			fmt.Fprintln(os.Stderr, "    - AI-generator metadata present")
		}
	}
	fmt.Fprintln(os.Stderr, "Run `aiwr clean-file <path> --in-place` (or use clean-staged) to strip these before committing.")
	return 1
}

func auditItemActionable(item map[string]any) bool {
	if value, _ := item["has_c2pa"].(bool); value {
		return true
	}
	confidence, _ := item["confidence"].([]string)
	for _, level := range confidence {
		if level == "confirmed" || level == "probable" {
			return true
		}
	}
	return false
}

func runCleanStaged(args []string) int {
	fs := newFlagSet("clean-staged")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return cliError(errors.New("clean-staged requires at least one file"))
	}

	changedPaths := []string{}
	failures := map[string]string{}
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "not a file: %s\n", path)
			continue
		}
		if st.Size() > core.MaxInputBytes() {
			fmt.Fprintf(os.Stderr, "skipping %s: larger than %d bytes\n", path, core.MaxInputBytes())
			continue
		}
		kind, classifyErr := core.Classify(path)
		if classifyErr != nil {
			failures[path] = classifyErr.Error()
			continue
		}
		if kind == core.KindUnknown {
			// Match clean_file.py's non-fatal unknown-format skip. Never mutate
			// an unrecognized staged binary just because it is in the hook list.
			continue
		}
		before, readErr := os.ReadFile(path)
		if readErr != nil {
			failures[path] = readErr.Error()
			continue
		}
		opts := core.DefaultOptions()
		opts.InPlace = true
		opts.Quiet = true
		result, cleanErr := core.CleanFile(path, opts)
		if cleanErr != nil {
			failures[path] = cleanErr.Error()
			continue
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			failures[path] = readErr.Error()
			continue
		}
		if !bytes.Equal(before, after) || result.Changed {
			changedPaths = append(changedPaths, path)
		}
	}

	if len(changedPaths) > 0 {
		fmt.Fprintf(os.Stderr, "aiwr: cleaned %d file(s) in place:\n", len(changedPaths))
		for _, path := range changedPaths {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
		fmt.Fprintln(os.Stderr, "Review the changes and re-stage before committing.")
	}
	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "aiwr: %d file(s) could not be cleaned:\n", len(failures))
		for _, path := range paths {
			if detail, ok := failures[path]; ok {
				fmt.Fprintf(os.Stderr, "  %s\n    - %s\n", path, detail)
				bak := path + ".bak"
				if _, err := os.Stat(bak); err == nil {
					fmt.Fprintf(os.Stderr, "    - backup left behind: %s\n", bak)
				}
			}
		}
		fmt.Fprintln(os.Stderr, "Those files were not confirmed clean. Re-run the cleaner by hand.")
		return 3
	}
	if len(changedPaths) > 0 {
		return 1
	}
	return 0
}

const maxHookPayloadBytes = 1 << 20

var hookFileWritingTools = map[string]bool{
	"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true, "Update": true,
}

type hookSpecificOutput struct {
	HookEventName string `json:"hookEventName"`
}

type hookMessage struct {
	SystemMessage     string             `json:"systemMessage"`
	HookSpecific      hookSpecificOutput `json:"hookSpecificOutput"`
	AdditionalContext string             `json:"additionalContext,omitempty"`
}

func runHookWrittenFile(args []string) int {
	mode := ""
	fs := newFlagSet("hook-written-file")
	fs.StringVar(&mode, "mode", mode, "hook mode: check or clean")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "aiwr: hook-written-file does not accept positional arguments")
		return 1
	}
	mode = hookMode(mode)

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxHookPayloadBytes+1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook payload read failed: %v\n", err)
		return 1
	}
	if int64(len(raw)) > maxHookPayloadBytes {
		fmt.Fprintln(os.Stderr, "aiwr: hook payload is too large")
		return 1
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook payload was not valid JSON: %v\n", err)
		return 1
	}
	path, ok := hookTargetPath(payload)
	if !ok {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > core.MaxInputBytes() {
		return 0
	}
	if mode == "check" {
		return hookCheck(path)
	}
	return hookClean(path, st.Mode().Perm())
}

func hookMode(requested string) string {
	for _, candidate := range []string{requested, os.Getenv("CLAUDE_PLUGIN_OPTION_HOOK_MODE"), os.Getenv("WATERMARKS_HOOK_MODE")} {
		value := strings.ToLower(strings.TrimSpace(candidate))
		if value == "" {
			continue
		}
		if value == "check" || value == "clean" {
			return value
		}
		fmt.Fprintf(os.Stderr, "aiwr: unknown hook mode %q; using check\n", value)
		return "check"
	}
	return "check"
}

func hookTargetPath(payload map[string]any) (string, bool) {
	tool, _ := payload["tool_name"].(string)
	if !hookFileWritingTools[tool] {
		return "", false
	}
	input, _ := payload["tool_input"].(map[string]any)
	if input == nil {
		return "", false
	}
	raw, _ := input["file_path"].(string)
	if strings.TrimSpace(raw) == "" {
		raw, _ = input["notebook_path"].(string)
	}
	if strings.TrimSpace(raw) == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
		}
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), true
	}
	cwd, _ := payload["cwd"].(string)
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	return filepath.Clean(filepath.Join(cwd, raw)), true
}

func hookCheck(path string) int {
	report, err := core.InspectFile(path, core.DefaultOptions())
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	if report.Kind == core.KindUnknown {
		return 0
	}
	item := auditItem(report, core.DefaultOptions())
	if !auditItemActionable(item) {
		return 0
	}
	findings, _ := item["findings"].([]string)
	detail := strings.Join(findings, "; ")
	if value, _ := item["has_c2pa"].(bool); value {
		if detail != "" {
			detail += "; "
		}
		detail += "C2PA manifest present"
	}
	if value, _ := item["has_ai_metadata"].(bool); value {
		if detail != "" {
			detail += "; "
		}
		detail += "AI-generator metadata present"
	}
	hookEmit(fmt.Sprintf("aiwr: %s carries provenance marks (%s)", filepath.Base(path), detail), "")
	fmt.Fprintf(os.Stderr, "aiwr: %s carries AI/C2PA provenance marks:\n", path)
	for _, finding := range findings {
		fmt.Fprintf(os.Stderr, "  - %s\n", finding)
	}
	fmt.Fprintln(os.Stderr, "Clean it with `aiwr clean-file <path> --in-place`, or set WATERMARKS_HOOK_MODE=clean.")
	return 2
}

func hookClean(path string, mode os.FileMode) int {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.aiwr-hook")
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	defer os.Remove(tempPath)

	opts := core.DefaultOptions()
	opts.Output = tempPath
	opts.Quiet = true
	result, err := core.CleanFile(path, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	cleaned, err := os.ReadFile(tempPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	original, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	residual := result.StillHasC2PA || result.StillHasAI
	if bytes.Equal(original, cleaned) {
		if residual {
			detail := strings.Join(result.PostFindings, "; ")
			if detail == "" {
				detail = hookDescribe(result)
			}
			hookEmit(fmt.Sprintf("aiwr: %s was left unchanged; cleanup left residual signals (%s)", filepath.Base(path), detail), "The aiwr hook could not confirm that the file is clean; it was left unchanged.")
		}
		return 0
	}
	if err := core.WriteFileAtomic(path, cleaned, mode); err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: hook failed on %s: %v\n", path, err)
		return 1
	}
	summary := hookDescribe(result)
	if residual {
		summary += "; residual provenance signals may remain"
	}
	hookEmit(fmt.Sprintf("aiwr: cleaned %s in place (%s)", filepath.Base(path), summary), fmt.Sprintf("The aiwr hook cleaned %s after the write; re-read it before editing again.", path))
	return 0
}

func hookDescribe(result core.CleanResult) string {
	parts := []string{}
	if result.Stats != nil {
		if n, ok := result.Stats["removed_count"].(int); ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%d character(s) removed", n))
		}
		if n, ok := result.Stats["replaced_count"].(int); ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%d replaced", n))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	if len(result.Actions) > 0 {
		return strings.Join(result.Actions, "; ")
	}
	return "metadata stripped"
}

func hookEmit(systemMessage, additionalContext string) {
	message := hookMessage{
		SystemMessage:     systemMessage,
		HookSpecific:      hookSpecificOutput{HookEventName: "PostToolUse"},
		AdditionalContext: additionalContext,
	}
	_ = json.NewEncoder(os.Stdout).Encode(message)
}
