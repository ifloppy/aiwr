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
			fmt.Fprintf(os.Stderr, "%s：%s\n", cliText("not a file", "不是文件"), path)
			return 2
		}
		if st.Size() > core.MaxInputBytes() {
			fmt.Fprintf(os.Stderr, "%s\n", cliText(
				fmt.Sprintf("skipping %s: larger than %d bytes", path, core.MaxInputBytes()),
				fmt.Sprintf("跳过 %s：大于 %d 字节", path, core.MaxInputBytes()),
			))
			continue
		}
		report, inspectErr := core.InspectFile(path, opts)
		if inspectErr != nil {
			fmt.Fprintf(os.Stderr, "aiwr: %s: %s\n", path, localizedError(inspectErr))
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
	fmt.Fprintf(os.Stderr, "%s\n", cliText(
		fmt.Sprintf("aiwr: %d file(s) carry AI/C2PA provenance marks:", len(actionable)),
		fmt.Sprintf("aiwr：%d 个文件包含 AI/C2PA 来源标记：", len(actionable)),
	))
	for _, item := range actionable {
		fmt.Fprintf(os.Stderr, "  %v\n", item["path"])
		if findings, ok := item["findings"].([]string); ok {
			for _, finding := range findings {
				fmt.Fprintf(os.Stderr, "    - %s\n", finding)
			}
		}
		if value, _ := item["has_c2pa"].(bool); value {
			fmt.Fprintln(os.Stderr, "    - "+cliText("C2PA manifest present", "存在 C2PA 清单"))
		}
		if value, _ := item["has_ai_metadata"].(bool); value {
			fmt.Fprintln(os.Stderr, "    - "+cliText("AI-generator metadata present", "存在 AI 生成器元数据"))
		}
	}
	fmt.Fprintln(os.Stderr, cliText("Run `aiwr clean-file <path> --in-place` (or use clean-staged) to strip these before committing.", "提交前请运行 `aiwr clean-file <path> --in-place`（或使用 clean-staged）移除这些标记。"))
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
			fmt.Fprintf(os.Stderr, "%s：%s\n", cliText("not a file", "不是文件"), path)
			continue
		}
		if st.Size() > core.MaxInputBytes() {
			fmt.Fprintf(os.Stderr, "%s\n", cliText(
				fmt.Sprintf("skipping %s: larger than %d bytes", path, core.MaxInputBytes()),
				fmt.Sprintf("跳过 %s：大于 %d 字节", path, core.MaxInputBytes()),
			))
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
		fmt.Fprintf(os.Stderr, "%s\n", cliText(
			fmt.Sprintf("aiwr: cleaned %d file(s) in place:", len(changedPaths)),
			fmt.Sprintf("aiwr：已原地清理 %d 个文件：", len(changedPaths)),
		))
		for _, path := range changedPaths {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
		fmt.Fprintln(os.Stderr, cliText("Review the changes and re-stage before committing.", "提交前请检查变更并重新暂存。"))
	}
	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "%s\n", cliText(
			fmt.Sprintf("aiwr: %d file(s) could not be cleaned:", len(failures)),
			fmt.Sprintf("aiwr：%d 个文件无法清理：", len(failures)),
		))
		for _, path := range paths {
			if detail, ok := failures[path]; ok {
				fmt.Fprintf(os.Stderr, "  %s\n    - %s\n", path, detail)
				bak := path + ".bak"
				if _, err := os.Stat(bak); err == nil {
					fmt.Fprintf(os.Stderr, "    - %s：%s\n", cliText("backup left behind", "已保留备份"), bak)
				}
			}
		}
		fmt.Fprintln(os.Stderr, cliText("Those files were not confirmed clean. Re-run the cleaner by hand.", "这些文件未确认清理干净，请手动重新运行清理命令。"))
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
		fmt.Fprintf(os.Stderr, "aiwr: %s：%s\n", cliText("hook payload read failed", "hook payload 读取失败"), localizedError(err))
		return 1
	}
	if int64(len(raw)) > maxHookPayloadBytes {
		fmt.Fprintln(os.Stderr, "aiwr: "+cliText("hook payload is too large", "hook payload 过大"))
		return 1
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %s：%s\n", cliText("hook payload was not valid JSON", "hook payload 不是有效 JSON"), localizedError(err))
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
		fmt.Fprintf(os.Stderr, "aiwr: %s %q；%s\n", cliText("unknown hook mode", "未知 hook 模式"), value, cliText("using check", "将使用 check"))
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
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
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
	fmt.Fprintf(os.Stderr, "%s\n", cliText(
		fmt.Sprintf("aiwr: %s carries AI/C2PA provenance marks:", path),
		fmt.Sprintf("aiwr：%s 包含 AI/C2PA 来源标记：", path),
	))
	for _, finding := range findings {
		fmt.Fprintf(os.Stderr, "  - %s\n", finding)
	}
	fmt.Fprintln(os.Stderr, cliText("Clean it with `aiwr clean-file <path> --in-place`, or set WATERMARKS_HOOK_MODE=clean.", "请使用 `aiwr clean-file <path> --in-place` 清理，或设置 WATERMARKS_HOOK_MODE=clean。"))
	return 2
}

func hookClean(path string, mode os.FileMode) int {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.aiwr-hook")
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
		return 1
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
		return 1
	}
	defer os.Remove(tempPath)

	opts := core.DefaultOptions()
	opts.Output = tempPath
	opts.Quiet = true
	result, err := core.CleanFile(path, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
		return 1
	}
	cleaned, err := os.ReadFile(tempPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
		return 1
	}
	original, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
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
		fmt.Fprintf(os.Stderr, "aiwr: %s %s：%s\n", cliText("hook failed on", "hook 处理失败："), path, localizedError(err))
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
	return cliText("metadata stripped", "元数据已清理")
}

func hookEmit(systemMessage, additionalContext string) {
	message := hookMessage{
		SystemMessage:     systemMessage,
		HookSpecific:      hookSpecificOutput{HookEventName: "PostToolUse"},
		AdditionalContext: additionalContext,
	}
	_ = json.NewEncoder(os.Stdout).Encode(message)
}
