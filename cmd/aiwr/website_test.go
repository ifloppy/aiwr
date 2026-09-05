package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iruanp/aiwr/internal/core"
)

func TestWebsiteSitemapParsingAndGuards(t *testing.T) {
	plain := []byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://example.com/a</loc></url></urlset>`)
	kind, urls, err := parseWebsiteSitemap(plain)
	if err != nil || kind != "urlset" || len(urls) != 1 || urls[0] != "https://example.com/a" {
		t.Fatalf("sitemap = %q %#v, err=%v", kind, urls, err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(plain)
	_ = writer.Close()
	if _, _, err := parseWebsiteSitemap(compressed.Bytes()); err != nil {
		t.Fatalf("gzip sitemap: %v", err)
	}
	for _, hostile := range []string{
		`<!DOCTYPE sitemapindex><sitemapindex/>`,
		`<!ENTITY x "boom"><urlset/>`,
	} {
		if _, _, err := parseWebsiteSitemap([]byte(hostile)); err == nil || !strings.Contains(err.Error(), "DTD / entities") {
			t.Fatalf("hostile sitemap accepted: %v", err)
		}
	}
}

func TestWebsiteKindRoutingAndRemoteInspection(t *testing.T) {
	if websiteAddressPublic(mustWebsiteAddr("93.184.216.34")) == false {
		t.Fatal("public IPv4 rejected")
	}
	if websiteAddressPublic(mustWebsiteAddr("10.0.0.8")) {
		t.Fatal("private IPv4 accepted")
	}
	if websiteGuessKind("https://example.com/photo.webp", []byte("not-used"), "image/webp") != "webp" {
		t.Fatal("content-type WebP routing failed")
	}
	if websiteGuessKind("https://example.com/asset", []byte("%PDF-1.7"), "") != "pdf" {
		t.Fatal("PDF magic routing failed")
	}
	if websiteGuessKind("https://example.com/page", []byte(`<html><head><meta name="generator" content="WordPress"></head></html>`), "text/html") != "html" {
		t.Fatal("HTML routing failed")
	}
	report, err := inspectWebsiteRemote("https://example.com/page", []byte(`<html><head><meta name="generator" content="WordPress"></head></html>`), "text/html", core.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if report.Format != "html" || len(report.Findings) != 1 || !strings.HasPrefix(report.Findings[0], "info: cms generator: ") || report.FindingsConfidence[0] != "informational" {
		t.Fatalf("remote HTML report = %#v", report)
	}
}

func TestWebsiteExtensionlessZipRouting(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"word/document.xml":   "<w:document/>",
		"[Content_Types].xml": "<Types/>",
	} {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(part, body)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := websiteGuessKind("https://example.com/download/asset", buffer.Bytes(), "application/octet-stream"); got != "docx" {
		t.Fatalf("extensionless ZIP kind = %q", got)
	}
}

func TestNativeStagedCleanerUsesCoreAndPreservesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	original := []byte("hello\u200bworld\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if code := runCheckStaged([]string{path}); code != 1 {
		t.Fatalf("check-staged code = %d", code)
	}
	if code := runCleanStaged([]string{path}); code != 1 {
		t.Fatalf("clean-staged code = %d", code)
	}
	cleaned, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(cleaned) != "helloworld\n" {
		t.Fatalf("cleaned text = %q", cleaned)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup = %q, want original", backup)
	}
	if code := runCheckStaged([]string{path}); code != 0 {
		t.Fatalf("clean file check-staged code = %d", code)
	}
}

func TestHookTargetAndJSONShape(t *testing.T) {
	path, ok := hookTargetPath(map[string]any{
		"tool_name":  "Write",
		"cwd":        "/tmp/project",
		"tool_input": map[string]any{"file_path": "docs/note.md"},
	})
	if !ok || path != filepath.Join("/tmp/project", "docs", "note.md") {
		t.Fatalf("hook target = %q, ok=%t", path, ok)
	}
	var message hookMessage
	encoded, err := json.Marshal(hookMessage{SystemMessage: "x", HookSpecific: hookSpecificOutput{HookEventName: "PostToolUse"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &message); err != nil || message.HookSpecific.HookEventName != "PostToolUse" {
		t.Fatalf("hook JSON = %s", encoded)
	}
}

func mustWebsiteAddr(raw string) netip.Addr {
	address, err := netip.ParseAddr(raw)
	if err != nil {
		panic(err)
	}
	return address
}
