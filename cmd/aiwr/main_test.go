package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iruanp/aiwr/internal/core"
)

func TestNormalizeFlagArgsKeepsRewriteValuesAfterPath(t *testing.T) {
	args := []string{
		"draft.txt",
		"--reasoning-effort", "high",
		"--markllm-dir", "/tmp/markllm",
		"--markllm-timeout", "30",
		"--target-margin", "0.1",
		"--noop-lex-floor", "0.2",
		"--markllm-scheme", "synthid",
		"--markllm-model", "model-name",
	}
	want := []string{
		"--reasoning-effort", "high",
		"--markllm-dir", "/tmp/markllm",
		"--markllm-timeout", "30",
		"--target-margin", "0.1",
		"--noop-lex-floor", "0.2",
		"--markllm-scheme", "synthid",
		"--markllm-model", "model-name",
		"draft.txt",
	}
	if got := normalizeFlagArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized args = %#v, want %#v", got, want)
	}
}

func TestNormalizeFlagArgsHonorsDoubleDash(t *testing.T) {
	args := []string{"--reasoning-effort", "low", "--", "--not-a-flag", "file.txt"}
	want := []string{"--reasoning-effort", "low", "--not-a-flag", "file.txt"}
	if got := normalizeFlagArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized args after -- = %#v, want %#v", got, want)
	}
}

func TestMediaOutputNames(t *testing.T) {
	if got := audioOutputName("/tmp/tone.ogg"); got != "tone.audio.m4a" {
		t.Fatalf("audio output name = %q, want tone.audio.m4a", got)
	}
	if got := mediaOutputName("/tmp/clip.mov", ".video"); got != "clip.video.mov" {
		t.Fatalf("video output name = %q, want clip.video.mov", got)
	}
}

func TestCLIHelpIsSuccessful(t *testing.T) {
	if got := cliError(flag.ErrHelp); got != 0 {
		t.Fatalf("cliError(flag.ErrHelp) = %d, want 0", got)
	}
}

func TestDetectCLILanguage(t *testing.T) {
	for _, name := range []string{"AIWR_LANG", "AIWR_LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		t.Setenv(name, "")
	}
	if got := detectCLILanguage(); got != cliEnglish {
		t.Fatalf("empty locale = %v, want English", got)
	}

	t.Setenv("LANG", "zh_CN.UTF-8")
	if got := detectCLILanguage(); got != cliChinese {
		t.Fatalf("Chinese LANG = %v, want Chinese", got)
	}

	t.Setenv("LC_ALL", "C")
	if got := detectCLILanguage(); got != cliEnglish {
		t.Fatalf("LC_ALL precedence = %v, want English", got)
	}

	t.Setenv("AIWR_LANG", "zh-Hans")
	if got := detectCLILanguage(); got != cliChinese {
		t.Fatalf("AIWR_LANG override = %v, want Chinese", got)
	}

	t.Setenv("AIWR_LANG", "fr_FR")
	if got := detectCLILanguage(); got != cliEnglish {
		t.Fatalf("unsupported locale = %v, want English fallback", got)
	}
}

func TestLocalizedUsage(t *testing.T) {
	t.Setenv("AIWR_LANG", "en_US.UTF-8")
	if usage := localizedUsage(); !strings.Contains(usage, "Usage:") || strings.Contains(usage, "用法：") {
		t.Fatalf("English usage = %q", usage)
	}
	t.Setenv("AIWR_LANG", "zh_CN.UTF-8")
	if usage := localizedUsage(); !strings.Contains(usage, "用法：") || strings.Contains(usage, "Usage:") {
		t.Fatalf("Chinese usage = %q", usage)
	}
}

func TestAuditKnownTextExtensionAllowsUTF16Bytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Localizable.strings")
	// UTF-16LE BOM + "Hi\n". The upstream audit reads this as UTF-8 with
	// surrogateescape and treats it as an ordinary, clean text item.
	data := []byte{0xff, 0xfe, 'H', 0, 'i', 0, '\n', 0}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	item, skipped := auditOne(path, core.DefaultOptions())
	if skipped != nil {
		t.Fatalf("UTF-16 text was skipped: %#v", skipped)
	}
	if item["kind"] != "text" || item["suspicious_total"] != 0 {
		t.Fatalf("unexpected UTF-16 audit item: %#v", item)
	}
}
