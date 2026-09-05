package core

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

var aiMetaNameRE = regexp.MustCompile(`(?i)generator|ai[-_ ]?generated|claude|anthropic|openai|gemini|synthid|c2pa|content.?credential|provenance|digital.?source|aigc`)
var htmlCleanMetaRE = regexp.MustCompile(`(?i)generator|claude|anthropic|openai|gemini|synthid|c2pa|aigc`)
var generatorAIRE = regexp.MustCompile(`(?i)\bai\b|claude|anthropic|openai|chatgpt|gemini|synthid|copilot|midjourney|dall.?e|stable.?diffusion`)
var metaTagRE = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
var metaAttrRE = regexp.MustCompile(`(?i)(name|property|content|generator)\s*=\s*["']([^"']*)["']`)
var jsonLDRE = regexp.MustCompile(`(?is)<script\b[^>]*type\s*=\s*["']application/ld\+json["'][^>]*>.*?</script\s*>`)
var dataAIAttrRE = regexp.MustCompile(`(?i)\sdata-ai[\w-]*\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
var dataAIInspectAttrRE = regexp.MustCompile(`(?i)\bdata-ai[\w-]*\s*=\s*["'][^"']*["']`)
var svgMetadataRE = regexp.MustCompile(`(?is)<metadata\b[^>]*>.*?</metadata\s*>`)
var svgXMPRE = regexp.MustCompile(`(?is)<x:xmpmeta\b[^>]*>.*?</x:xmpmeta\s*>`)
var svgCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
var xmlDeclRE = regexp.MustCompile(`(?is)<!DOCTYPE(?:\s|\[).*?>|<!ENTITY(?:\s|\[).*?>`)
var svgRootRE = regexp.MustCompile(`(?is)<svg\b[^>]*>`)
var pdfXMPRE = regexp.MustCompile(`(?is)<(?:x:xmpmeta|xmpmeta)\b.*?</(?:x:xmpmeta|xmpmeta)\s*>`)
var pdfFieldRE = regexp.MustCompile(`(?is)/(?:Producer|Creator|Author|Subject|Keywords|Title)\s*\([^)]*\)`)
var epubContentRE = regexp.MustCompile(`(?i)\.(xhtml|html?|css|js|ncx|opf|svg|png|jpe?g|webp|avif|heic|gif|bmp|tiff?|ttf|otf|woff2?)$`)

var aiFrontmatterKeys = map[string]bool{
	"generator": true, "ai": true, "ai_generated": true, "ai-generated": true, "claude": true,
	"anthropic": true, "openai": true, "gemini": true, "synthid": true, "c2pa": true,
	"content_credentials": true, "contentcredentials": true, "provenance": true,
	"digital_source_type": true, "digitalsourcetype": true, "created_with": true,
	"createdwith": true, "model": true, "llm": true,
}

var markdownFrontmatterRE = regexp.MustCompile(`(?s)\A(---\r?\n)(.*?)(\r?\n---\r?\n?)`)
var markdownTopLevelKeyRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:`)
var xmlTextSegmentTagRE = regexp.MustCompile(`(?s)<[^>]+>`)

func inspectContainer(data []byte, path, format string, options ...Options) FileReport {
	report, _ := inspectContainerWithError(data, path, format, options...)
	return report
}

func inspectContainerWithError(data []byte, path, format string, options ...Options) (FileReport, error) {
	opts := DefaultOptions()
	if len(options) > 0 {
		opts = options[0]
	}
	report := FileReport{Kind: KindContainer, Path: path, Format: format}
	switch format {
	case "markdown":
		report.HasC2PA, report.HasAIMetadata, report.Findings, report.Details = inspectMarkdown(data)
		textReport := inspectText(data, opts.AggressiveHomoglyphs, opts.StripEmojiGlue)
		if opts.Stylometry {
			textReport.Stylometry = scoreStylometry(string(data), path, opts.Threshold)
		}
		report.Text = &textReport
		report.LayerAHits = textReport.Hits
		report.SuspiciousTotal = textReport.SuspiciousTotal
		appendLayerAFinding(&report, "")
	case "html":
		report.HasC2PA, report.HasAIMetadata, report.Findings, report.Details = inspectHTML(data)
		textReport := inspectText(data, opts.AggressiveHomoglyphs, opts.StripEmojiGlue)
		if opts.Stylometry {
			textReport.Stylometry = scoreStylometry(string(data), path, opts.Threshold)
		}
		report.Text = &textReport
		report.LayerAHits = textReport.Hits
		report.SuspiciousTotal = textReport.SuspiciousTotal
		appendLayerAFinding(&report, "")
	case "svg":
		report.HasC2PA, report.HasAIMetadata, report.Findings, report.Details = inspectSVG(data)
	case "pdf":
		report.HasC2PA, report.HasAIMetadata, report.Findings, report.Details = inspectPDF(data, path)
		if tools, ok := report.Details["tools"].(map[string]any); ok {
			report.Tools = tools
			delete(report.Details, "tools")
		}
	case "docx", "xlsx", "pptx", "odt", "epub":
		var zipBudget int64
		var inspectErr error
		if format == "docx" || format == "xlsx" || format == "pptx" {
			report.HasC2PA, report.HasAIMetadata, report.Findings, report.Details, inspectErr = inspectZipContainerWithBudget(data, format, &zipBudget, opts)
		} else {
			report.HasC2PA, report.HasAIMetadata, report.Findings, report.Details, inspectErr = inspectZipContainerWithBudget(data, format, nil, opts)
		}
		if inspectErr != nil {
			return report, inspectErr
		}
		var total int
		var hits []TextHit
		var layerFindings []string
		if format == "docx" || format == "xlsx" || format == "pptx" {
			total, hits, layerFindings, inspectErr = inspectZipLayerAWithBudget(data, format, opts, &zipBudget)
		} else {
			total, hits, layerFindings, inspectErr = inspectZipLayerAWithBudget(data, format, opts, nil)
		}
		if inspectErr != nil {
			return report, inspectErr
		}
		report.SuspiciousTotal = total
		report.LayerAHits = hits
		report.Findings = append(report.Findings, layerFindings...)
	default:
		report.Findings = []string{fmt.Sprintf("unsupported container: %s", format)}
	}
	switch format {
	case "pdf":
		report.Notes = append(report.Notes, "PDF inspection is best-effort; exiftool/c2patool give more reliable metadata detection")
	case "docx", "xlsx", "pptx":
		report.Notes = append(report.Notes, strings.ToUpper(format)+": metadata/provenance and embedded media are scanned")
	case "epub":
		report.Notes = append(report.Notes, "EPUB: package-document metadata, XHTML meta/JSON-LD, and embedded media are scanned")
	}
	if report.SuspiciousTotal > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("layer A: %d invisible/format codepoint(s) in body text; clean removes these", report.SuspiciousTotal))
	}
	// The upstream container inspector records optional tool availability for
	// these formats, but only the PDF/image paths use the probe to change the
	// verdict or append a finding. Keep the inventory without changing the
	// native container findings.
	if !opts.DisableExternalTools && len(report.Tools) == 0 && (format == "svg" || format == "pdf" || format == "docx" || format == "xlsx" || format == "pptx") {
		report.Tools = inspectOptionalTools(path, data)
	}
	for _, f := range report.Findings {
		report.FindingsConfidence = append(report.FindingsConfidence, findingConfidence(f))
	}
	return report, nil
}

func appendLayerAFinding(report *FileReport, part string) {
	for _, hit := range report.LayerAHits {
		label := fmt.Sprintf("%s %s x%d (%s)", hit.Codepoint, hit.Label, hit.Count, hit.Kind)
		if part != "" {
			report.Findings = append(report.Findings, "layer-a ("+part+"): "+label)
		} else {
			report.Findings = append(report.Findings, "layer-a: "+label)
		}
	}
}

type zipBudgetExceededError struct {
	member string
}

func (e *zipBudgetExceededError) Error() string {
	if e.member == "" {
		return fmt.Sprintf("zip decompressed size exceeds cap (%d bytes)", DefaultMaxZipBytes)
	}
	return fmt.Sprintf("zip member %s decompressed size exceeds cap (%d bytes)", e.member, DefaultMaxZipBytes)
}

func isZipBudgetExceeded(err error) bool {
	_, ok := err.(*zipBudgetExceededError)
	return ok
}

func checkZipMemberDeclaredSize(f *zip.File) error {
	if f.UncompressedSize64 > uint64(DefaultMaxZipBytes) {
		return &zipBudgetExceededError{member: f.Name}
	}
	return nil
}

func readZipMemberBounded(f *zip.File, budget *int64) ([]byte, error) {
	if budget == nil {
		local := int64(0)
		budget = &local
	}
	if err := checkZipMemberDeclaredSize(f); err != nil {
		return nil, err
	}
	if *budget < 0 || *budget >= DefaultMaxZipBytes {
		return nil, &zipBudgetExceededError{member: f.Name}
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	remaining := DefaultMaxZipBytes - *budget
	raw, readErr := io.ReadAll(io.LimitReader(r, remaining+1))
	closeErr := r.Close()
	if readErr == nil {
		readErr = closeErr
	}
	*budget += int64(len(raw))
	if *budget > DefaultMaxZipBytes {
		return raw, &zipBudgetExceededError{member: f.Name}
	}
	return raw, readErr
}

func inspectZipLayerA(data []byte, format string, opts Options) (int, []TextHit, []string) {
	total, hits, findings, _ := inspectZipLayerAWithBudget(data, format, opts, nil)
	return total, hits, findings
}

func inspectZipLayerAWithBudget(data []byte, format string, opts Options, sharedBudget *int64) (int, []TextHit, []string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, nil, nil, nil
	}
	total := 0
	allHits := []TextHit{}
	findings := []string{}
	budget := sharedBudget
	if budget == nil {
		budget = new(int64)
	}
	encrypted := map[string]bool{}
	if format == "epub" {
		encrypted = encryptedEPUBMembers(data)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if encrypted[name] || encrypted[strings.ToLower(name)] {
			continue
		}
		isBodyPart := (format == "docx" && strings.HasPrefix(name, "word/") && strings.HasSuffix(name, ".xml")) ||
			(format == "xlsx" && strings.HasPrefix(name, "xl/") && strings.HasSuffix(name, ".xml")) ||
			(format == "pptx" && strings.HasPrefix(name, "ppt/") && strings.HasSuffix(name, ".xml")) ||
			(format == "odt" && name == "content.xml") ||
			(format == "epub" && isEPUBHTMLName(name))
		if !isBodyPart {
			continue
		}
		if err := checkZipMemberDeclaredSize(f); err != nil {
			return total, allHits, findings, err
		}
		raw, readErr := readZipMemberBounded(f, budget)
		if isZipBudgetExceeded(readErr) {
			return total, allHits, findings, readErr
		}
		if readErr != nil {
			break
		}
		var textParts []string
		switch {
		case format == "docx" && strings.HasPrefix(name, "word/") && strings.HasSuffix(name, ".xml"):
			textParts = xmlTextRuns(raw, "w:t")
		case format == "xlsx" && strings.HasPrefix(name, "xl/") && strings.HasSuffix(name, ".xml"):
			textParts = xmlTextRuns(raw, "t")
		case format == "pptx" && strings.HasPrefix(name, "ppt/") && strings.HasSuffix(name, ".xml"):
			textParts = xmlTextRuns(raw, "a:t")
		case format == "odt" && name == "content.xml":
			textParts = xmlParagraphTexts(raw)
		case format == "epub" && isEPUBHTMLName(name):
			textParts = []string{string(raw)}
		}
		for _, part := range textParts {
			decoded := part
			if format != "epub" {
				decoded = decodeXMLEntities(part)
			}
			inner := inspectText([]byte(decoded), opts.AggressiveHomoglyphs, opts.StripEmojiGlue)
			total += inner.SuspiciousTotal
			allHits = append(allHits, inner.Hits...)
			for _, hit := range inner.Hits {
				label := fmt.Sprintf("%s %s x%d (%s)", hit.Codepoint, hit.Label, hit.Count, hit.Kind)
				if format == "epub" {
					findings = append(findings, "layer-a ("+name+"): "+label)
				} else {
					findings = append(findings, "layer-a ("+name+"): "+label)
				}
			}
		}
	}
	return total, allHits, findings, nil
}

func isXMLNameBoundary(value byte) bool {
	return value == '>' || value == '/' || value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func xmlTextRuns(raw []byte, element string) []string {
	text := string(raw)
	openNeedle := "<" + element
	closeNeedle := "</" + element
	lower := strings.ToLower(text)
	openLower := strings.ToLower(openNeedle)
	closeLower := strings.ToLower(closeNeedle)
	parts := []string{}
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], openLower)
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len(openNeedle)
		if boundary >= len(text) || !isXMLNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		openEnd := xmlTagEnd(text, start)
		if openEnd < 0 {
			break
		}
		closeStart := strings.Index(lower[openEnd:], closeLower)
		if closeStart < 0 {
			break
		}
		closeStart += openEnd
		if closeStart+len(closeNeedle) >= len(text) || !isXMLNameBoundary(text[closeStart+len(closeNeedle)]) {
			pos = closeStart + len(closeNeedle)
			continue
		}
		closeEnd := xmlTagEnd(text, closeStart)
		if closeEnd < 0 {
			break
		}
		parts = append(parts, text[openEnd:closeStart])
		pos = closeEnd
	}
	return parts
}

func xmlTagEnd(text string, start int) int {
	if start < 0 || start >= len(text) || text[start] != '<' {
		return -1
	}
	var quote byte
	for i := start + 1; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '>' {
			return i + 1
		}
	}
	return -1
}

// decodeXMLEntities resolves only the five predefined XML entities and valid
// numeric character references. HTML5-only names (for example &nbsp;) remain
// literal, matching the behavior of an XML parser.
func decodeXMLEntities(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] != '&' {
			out.WriteByte(text[i])
			i++
			continue
		}
		end := i + 1
		for end < len(text) && text[end] != ';' && text[end] != '&' {
			end++
		}
		if end < len(text) && text[end] == ';' {
			candidate := text[i+1 : end]
			if decoded, ok := decodeXMLReference(candidate); ok {
				out.WriteString(decoded)
				i = end + 1
				continue
			}
			out.WriteString(text[i : end+1])
			i = end + 1
			continue
		}
		out.WriteByte('&')
		i++
	}
	return out.String()
}

func decodeXMLReference(candidate string) (string, bool) {
	switch candidate {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return "\"", true
	case "apos":
		return "'", true
	}
	base := 10
	digits := candidate
	if strings.HasPrefix(candidate, "#x") || strings.HasPrefix(candidate, "#X") {
		base = 16
		digits = candidate[2:]
	} else if strings.HasPrefix(candidate, "#") {
		digits = candidate[1:]
	} else {
		return "", false
	}
	if digits == "" {
		return "", false
	}
	var value uint64
	for _, digit := range []byte(digits) {
		var n byte
		switch {
		case digit >= '0' && digit <= '9':
			n = digit - '0'
		case base == 16 && digit >= 'a' && digit <= 'f':
			n = digit - 'a' + 10
		case base == 16 && digit >= 'A' && digit <= 'F':
			n = digit - 'A' + 10
		default:
			return "", false
		}
		if uint64(n) >= uint64(base) || value > (0x10ffff-uint64(n))/uint64(base) {
			return "", false
		}
		value = value*uint64(base) + uint64(n)
	}
	if value == 0 || value > 0x10ffff || (value >= 0xd800 && value <= 0xdfff) {
		return "", false
	}
	// XML 1.0 permits tab, LF, CR, and the ranges below. Keep invalid
	// references literal so we do not manufacture a document a real parser
	// would reject.
	if value < 0x20 && value != 0x9 && value != 0xa && value != 0xd {
		return "", false
	}
	if value >= 0xfdd0 && value <= 0xfdef {
		return "", false
	}
	if value&0xffff == 0xfffe || value&0xffff == 0xffff {
		return "", false
	}
	return string(rune(value)), true
}

func xmlParagraphTexts(raw []byte) []string {
	paragraphs := xmlTextRuns(raw, "text:p")
	parts := []string{}
	for _, paragraph := range paragraphs {
		// Match the upstream ODT inspector's re.split(r"(<[^>]+>)", ...):
		// inspect each visible text segment independently so suspicious
		// carriers cannot be manufactured by concatenating text on opposite
		// sides of a span/tab element.
		last := 0
		for _, match := range xmlTextSegmentTagRE.FindAllStringIndex(paragraph, -1) {
			if match[0] > last {
				parts = append(parts, paragraph[last:match[0]])
			}
			last = match[1]
		}
		if last < len(paragraph) {
			parts = append(parts, paragraph[last:])
		}
	}
	return parts
}

func isEPUBHTMLName(name string) bool {
	low := strings.ToLower(name)
	return strings.HasSuffix(low, ".xhtml") || strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".htm")
}

func isEPUBMediaName(name string) bool {
	return regexp.MustCompile(`(?i)\.(png|jpe?g|webp|avif|heic|gif|bmp|tiff?|svg)$`).MatchString(name)
}

func embeddedImageFormat(data []byte, name string) string {
	if format := detectImageFormat(data); format != "unknown" {
		return format
	}
	if strings.HasSuffix(strings.ToLower(name), ".svg") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("<svg")) {
		return "svg"
	}
	return "unknown"
}

func inspectEmbeddedImage(data []byte, name string, opts Options) FileReport {
	if embeddedImageFormat(data, name) == "svg" {
		c2pa, ai, findings, details := inspectSVG(data)
		return FileReport{Kind: KindContainer, Path: name, Format: "svg", HasC2PA: c2pa, HasAIMetadata: ai, Findings: findings, Details: details}
	}
	return inspectImageWithOptions(data, name, opts, false)
}

func cleanEmbeddedImage(data []byte, name, format string, opts Options) ([]byte, []string, error) {
	if format == "svg" {
		return cleanSVG(data, opts)
	}
	return cleanImage(data, format, opts)
}

func acceptEmbeddedImageClean(raw, cleaned []byte, name string, imageActions []string) ([]byte, []string) {
	if bytes.Equal(raw, cleaned) || len(imageActions) == 0 {
		return raw, nil
	}
	for _, action := range imageActions {
		if !strings.Contains(strings.ToLower(action), "drop") {
			continue
		}
		limit := minInt(2, len(imageActions))
		return cleaned, []string{fmt.Sprintf("clean embedded media in %s (%s)", name, strings.Join(imageActions[:limit], ", "))}
	}
	return raw, nil
}

func cleanContainer(data []byte, format string, opts Options) ([]byte, []string, error) {
	switch format {
	case "markdown":
		cleaned, actions, err := cleanMarkdown(data, opts)
		if err != nil {
			return data, actions, err
		}
		return cleanContainerTextLayer(cleaned, actions, opts)
	case "html":
		cleaned, actions, err := cleanHTML(data, opts)
		if err != nil {
			return data, actions, err
		}
		return cleanContainerTextLayer(cleaned, actions, opts)
	case "svg":
		return cleanSVG(data, opts)
	case "pdf":
		return cleanPDF(data, opts)
	case "docx", "xlsx", "pptx", "odt", "epub":
		return cleanZipContainer(data, format, opts)
	default:
		return data, nil, fmt.Errorf("unsupported container format: %s", format)
	}
}

func cleanContainerTextLayer(data []byte, actions []string, opts Options) ([]byte, []string, error) {
	if !opts.AlsoLayerAText {
		return data, actions, nil
	}
	cleaned, removed, replaced := cleanContainerTextLayerStats(data, opts)
	if removed > 0 {
		actions = append(actions, fmt.Sprintf("Layer A text: removed=%d", removed))
	}
	if replaced > 0 {
		actions = append(actions, fmt.Sprintf("Layer A text: replaced=%d", replaced))
	}
	return cleaned, actions, nil
}

func cleanContainerTextLayerStats(data []byte, opts Options) ([]byte, int, int) {
	cleaned, stats := cleanText(data, opts)
	removed, _ := stats["removed_count"].(int)
	replaced, _ := stats["replaced_count"].(int)
	return cleaned, removed, replaced
}

func inspectMarkdown(data []byte) (bool, bool, []string, map[string]any) {
	text := string(data)
	findings := []string{}
	var hasAI, hasC2PA, hasFrontmatter bool
	keys := []string{}
	if m := markdownFrontmatterRE.FindStringSubmatch(text); m != nil {
		hasFrontmatter = true
		for _, line := range strings.Split(strings.ReplaceAll(m[2], "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
				continue
			}
			parts := markdownTopLevelKeyRE.FindStringSubmatch(line)
			if len(parts) == 0 {
				continue
			}
			key := parts[1]
			keys = append(keys, key)
			value := line[strings.Index(line, ":")+1:]
			if aiFrontmatterKeys[strings.ToLower(key)] || aiMetaNameRE.MatchString(key) {
				hasAI = true
				findings = append(findings, "frontmatter key: "+key)
			}
			if aiMetaNameRE.MatchString(value) {
				hasAI = true
				findings = append(findings, "frontmatter value hit on "+key)
			}
		}
	}
	c2, ai, more := inspectEmbeddedDataURIs(text)
	hasC2PA = c2
	hasAI = hasAI || ai
	findings = append(findings, more...)
	if !hasC2PA {
		for _, finding := range findings {
			lower := strings.ToLower(finding)
			if strings.Contains(lower, "c2pa") || strings.Contains(lower, "content") {
				hasC2PA = true
				break
			}
		}
	}
	return hasC2PA, hasAI || hasC2PA, findings, map[string]any{"has_frontmatter": hasFrontmatter, "keys": keys}
}

func cleanMarkdown(data []byte, opts Options) ([]byte, []string, error) {
	text := string(data)
	actions := []string{}
	if m := markdownFrontmatterRE.FindStringSubmatchIndex(text); m != nil {
		block := strings.ReplaceAll(text[m[4]:m[5]], "\r\n", "\n")
		lines := strings.Split(block, "\n")
		kept := []string{}
		dropping := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				if !dropping {
					kept = append(kept, line)
				}
				continue
			}
			if line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
				if !dropping {
					kept = append(kept, line)
				}
				continue
			}
			parts := markdownTopLevelKeyRE.FindStringSubmatch(line)
			if len(parts) == 0 {
				dropping = false
				kept = append(kept, line)
				continue
			}
			key := parts[1]
			value := line[strings.Index(line, ":")+1:]
			if aiFrontmatterKeys[strings.ToLower(key)] || aiMetaNameRE.MatchString(key) {
				actions = append(actions, "drop frontmatter key: "+key)
				dropping = true
				continue
			}
			if aiMetaNameRE.MatchString(value) {
				actions = append(actions, "drop frontmatter key (value hit): "+key)
				dropping = true
				continue
			}
			dropping = false
			kept = append(kept, line)
		}
		newBlock := strings.Trim(strings.Join(kept, "\n"), "\n")
		body := text[m[1]:]
		if newBlock != "" {
			text = "---\n" + newBlock + "\n---\n" + body
		} else {
			text = strings.TrimLeft(body, "\n")
			actions = append(actions, "removed empty frontmatter block")
		}
	}
	text, uriActions := cleanEmbeddedDataURIs(text, opts)
	actions = append(actions, uriActions...)
	if len(actions) == 0 {
		actions = append(actions, "no AI frontmatter keys or embedded data URIs removed")
	}
	return []byte(text), actions, nil
}

func isCMSGenerator(tag string) bool {
	attrs := map[string]string{}
	for _, match := range metaAttrRE.FindAllStringSubmatch(tag, -1) {
		if len(match) == 3 {
			attrs[strings.ToLower(match[1])] = match[2]
		}
	}
	nameOrProperty := strings.ToLower(attrs["name"])
	if nameOrProperty == "" {
		nameOrProperty = strings.ToLower(attrs["property"])
	}
	if nameOrProperty == "" {
		nameOrProperty = strings.ToLower(attrs["generator"])
	}
	if nameOrProperty != "generator" {
		return false
	}
	// A generator meta tag is CMS provenance unless its content names a
	// known AI generator. Match the upstream rule rather than maintaining a
	// closed list of CMS products: ordinary and future CMS names stay intact.
	return !generatorAIRE.MatchString(attrs["content"]) && !generatorAIRE.MatchString(tag)
}

const htmlSpaceChars = " \t\r\n\f"

func isHTMLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func isHTMLTagNameBoundary(value byte) bool {
	return isHTMLSpace(value) || value == '>' || value == '/' || value == '<'
}

func scanHTMLTagEnd(text string, start int) (int, bool) {
	if start < 0 || start >= len(text) || text[start] != '<' {
		return start, false
	}
	var quote byte
	for i := start + 1; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '>' {
			return i + 1, true
		}
	}
	return len(text), false
}

type textBlock struct {
	openStart  int
	openEnd    int
	closeStart int
	closeEnd   int
}

func htmlNamedBlocks(text, name string) []textBlock {
	if name == "" {
		return nil
	}
	lower := strings.ToLower(text)
	needle := "<" + strings.ToLower(name)
	closeNeedle := "</" + strings.ToLower(name)
	closes := make([]textBlock, 0)
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], closeNeedle)
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len(closeNeedle)
		if boundary >= len(text) || !isHTMLTagNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		end, closed := scanHTMLTagEnd(text, start)
		if !closed {
			break
		}
		// Closing tags may contain only whitespace before '>' for the named
		// block. This prevents </metadatax> and malformed tags from pairing.
		if strings.TrimSpace(text[boundary:end-1]) != "" {
			pos = end
			continue
		}
		closes = append(closes, textBlock{closeStart: start, closeEnd: end})
		pos = end
	}
	blocks := make([]textBlock, 0)
	closeIndex := 0
	lastEnd := 0
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], needle)
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len(needle)
		if boundary >= len(text) || !isHTMLTagNameBoundary(text[boundary]) || text[boundary] == '/' {
			pos = boundary
			continue
		}
		openEnd, closed := scanHTMLTagEnd(text, start)
		if !closed {
			break
		}
		if start < lastEnd {
			pos = openEnd
			continue
		}
		for closeIndex < len(closes) && closes[closeIndex].closeStart < openEnd {
			closeIndex++
		}
		if closeIndex >= len(closes) {
			break
		}
		close := closes[closeIndex]
		blocks = append(blocks, textBlock{openStart: start, openEnd: openEnd, closeStart: close.closeStart, closeEnd: close.closeEnd})
		lastEnd = close.closeEnd
		pos = openEnd
	}
	return blocks
}

func htmlScriptBlocks(text string) []textBlock {
	lower := strings.ToLower(text)
	closes := make([]textBlock, 0)
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], "</script")
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len("</script")
		if boundary >= len(text) || !isHTMLTagNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		end, closed := scanHTMLTagEnd(text, start)
		if !closed {
			break
		}
		closes = append(closes, textBlock{closeStart: start, closeEnd: end})
		pos = end
	}
	blocks := make([]textBlock, 0)
	closeIndex := 0
	lastEnd := 0
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], "<script")
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len("<script")
		if boundary >= len(text) || !isHTMLTagNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		openEnd, closed := scanHTMLTagEnd(text, start)
		if !closed {
			break
		}
		if start < lastEnd {
			pos = openEnd
			continue
		}
		for closeIndex < len(closes) && closes[closeIndex].closeStart < openEnd {
			closeIndex++
		}
		if closeIndex >= len(closes) {
			break
		}
		close := closes[closeIndex]
		blocks = append(blocks, textBlock{openStart: start, openEnd: openEnd, closeStart: close.closeStart, closeEnd: close.closeEnd})
		lastEnd = close.closeEnd
		pos = openEnd
	}
	return blocks
}

func scriptTagIsJSONLD(openTag string) bool {
	i := 0
	for i < len(openTag) && openTag[i] != ' ' && !isHTMLSpace(openTag[i]) && openTag[i] != '/' && openTag[i] != '>' {
		i++
	}
	for i < len(openTag) {
		for i < len(openTag) && isHTMLSpace(openTag[i]) {
			i++
		}
		if i >= len(openTag) || openTag[i] == '>' || openTag[i] == '/' {
			return false
		}
		nameStart := i
		for i < len(openTag) && openTag[i] != '=' && !isHTMLSpace(openTag[i]) && openTag[i] != '/' && openTag[i] != '>' {
			i++
		}
		name := strings.ToLower(openTag[nameStart:i])
		for i < len(openTag) && isHTMLSpace(openTag[i]) {
			i++
		}
		value := ""
		if i < len(openTag) && openTag[i] == '=' {
			i++
			for i < len(openTag) && isHTMLSpace(openTag[i]) {
				i++
			}
			if i < len(openTag) && (openTag[i] == '\'' || openTag[i] == '"') {
				quote := openTag[i]
				i++
				valueStart := i
				for i < len(openTag) && openTag[i] != quote {
					i++
				}
				value = openTag[valueStart:i]
				if i < len(openTag) {
					i++
				}
			} else {
				valueStart := i
				for i < len(openTag) && !isHTMLSpace(openTag[i]) && openTag[i] != '>' {
					i++
				}
				value = openTag[valueStart:i]
			}
		}
		if name == "type" && strings.EqualFold(value, "application/ld+json") {
			return true
		}
	}
	return false
}

func htmlMetaTags(text string) []textBlock {
	lower := strings.ToLower(text)
	tags := make([]textBlock, 0)
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], "<meta")
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len("<meta")
		if boundary >= len(text) || !isHTMLTagNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		end, closed := scanHTMLTagEnd(text, start)
		if !closed {
			break
		}
		tags = append(tags, textBlock{openStart: start, openEnd: end})
		pos = end
	}
	return tags
}

func htmlAttrNameChar(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == ':' || value == '-' || value == '.'
}

func isDataAIAttribute(name string) bool {
	return len(name) >= len("data-ai") && strings.EqualFold(name[:len("data-ai")], "data-ai")
}

func cleanHTMLTagDataAI(tag string) (string, int) {
	if len(tag) < 3 || tag[0] != '<' || tag[1] == '/' || tag[1] == '!' || tag[1] == '?' {
		return tag, 0
	}
	i := 1
	for i < len(tag) && !isHTMLSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	if i <= 1 {
		return tag, 0
	}
	var out strings.Builder
	out.WriteString(tag[:i])
	removed := 0
	for i < len(tag) {
		attrStart := i
		for i < len(tag) && isHTMLSpace(tag[i]) {
			i++
		}
		if i >= len(tag) {
			out.WriteString(tag[attrStart:])
			break
		}
		if tag[i] == '>' || (tag[i] == '/' && i+1 < len(tag) && tag[i+1] == '>') {
			out.WriteString(tag[attrStart:])
			break
		}
		nameStart := i
		for i < len(tag) && htmlAttrNameChar(tag[i]) {
			i++
		}
		if nameStart == i {
			out.WriteString(tag[attrStart : i+1])
			i++
			continue
		}
		name := tag[nameStart:i]
		for i < len(tag) && isHTMLSpace(tag[i]) {
			i++
		}
		valueEnd := i
		hasValue := false
		if i < len(tag) && tag[i] == '=' {
			hasValue = true
			i++
			for i < len(tag) && isHTMLSpace(tag[i]) {
				i++
			}
			if i < len(tag) && (tag[i] == '\'' || tag[i] == '"') {
				quote := tag[i]
				i++
				for i < len(tag) && tag[i] != quote {
					i++
				}
				if i < len(tag) {
					i++
				}
			} else {
				for i < len(tag) && !isHTMLSpace(tag[i]) && tag[i] != '>' {
					i++
				}
			}
			valueEnd = i
		}
		if isDataAIAttribute(name) && hasValue {
			removed++
			continue
		}
		out.WriteString(tag[attrStart:valueEnd])
		if !hasValue {
			out.WriteString(tag[valueEnd:i])
		}
	}
	return out.String(), removed
}

func cleanHTMLDataAIAttributes(text string) (string, int) {
	var out strings.Builder
	last := 0
	removed := 0
	for i := 0; i < len(text); {
		start := strings.IndexByte(text[i:], '<')
		if start < 0 {
			break
		}
		start += i
		end, closed := scanHTMLTagEnd(text, start)
		if !closed {
			break
		}
		out.WriteString(text[last:start])
		cleaned, count := cleanHTMLTagDataAI(text[start:end])
		out.WriteString(cleaned)
		removed += count
		last = end
		i = end
	}
	if last == 0 {
		return text, 0
	}
	out.WriteString(text[last:])
	return out.String(), removed
}

func dropTextBlocks(text string, blocks []textBlock, predicate func(string) bool) (string, int) {
	var out strings.Builder
	last, removed := 0, 0
	for _, block := range blocks {
		if !predicate(text[block.openStart:block.closeEnd]) {
			continue
		}
		out.WriteString(text[last:block.openStart])
		last = block.closeEnd
		removed++
	}
	if removed == 0 {
		return text, 0
	}
	out.WriteString(text[last:])
	return out.String(), removed
}

func replaceTextBlocks(text string, blocks []textBlock, replacement func(string) (string, bool)) (string, int) {
	var out strings.Builder
	last, replaced := 0, 0
	for _, block := range blocks {
		if block.openStart < last || block.closeEnd > len(text) {
			continue
		}
		value, ok := replacement(text[block.openStart:block.closeEnd])
		if !ok {
			continue
		}
		out.WriteString(text[last:block.openStart])
		out.WriteString(value)
		last = block.closeEnd
		replaced++
	}
	if replaced == 0 {
		return text, 0
	}
	out.WriteString(text[last:])
	return out.String(), replaced
}

func commentBlocks(text string) []textBlock {
	blocks := []textBlock{}
	for pos := 0; pos < len(text); {
		start := strings.Index(text[pos:], "<!--")
		if start < 0 {
			break
		}
		start += pos
		closeStart := strings.Index(text[start+4:], "-->")
		closeEnd := strings.Index(text[start+4:], "--!>")
		if closeStart < 0 || (closeEnd >= 0 && closeEnd < closeStart) {
			if closeEnd < 0 {
				break
			}
			closeStart = closeEnd
			closeEnd = start + 4 + closeStart + 4
		} else {
			closeEnd = start + 4 + closeStart + 3
		}
		blocks = append(blocks, textBlock{openStart: start, openEnd: start + 4, closeStart: start + 4 + closeStart, closeEnd: closeEnd})
		pos = closeEnd
	}
	return blocks
}

func xmlDeclarationKeyword(text string, start int) string {
	if start < 0 || start+2 > len(text) || text[start:start+2] != "<!" {
		return ""
	}
	for _, keyword := range []string{"doctype", "entity"} {
		end := start + 2 + len(keyword)
		if end <= len(text) && strings.EqualFold(text[start+2:end], keyword) {
			if end == len(text) || !(isXMLNameByte(text[end])) {
				return keyword
			}
		}
	}
	return ""
}

func isXMLNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == ':'
}

func xmlDeclarationEnd(text string, start int, keyword string) int {
	i := start + 2 + len(keyword)
	depth := 0
	var quote byte
	for i < len(text) {
		if quote != 0 {
			if text[i] == quote {
				quote = 0
			}
			i++
			continue
		}
		if strings.HasPrefix(text[i:], "<!--") {
			end := strings.Index(text[i+4:], "-->")
			if end < 0 {
				return -1
			}
			i += 4 + end + 3
			continue
		}
		switch text[i] {
		case '\'', '"':
			quote = text[i]
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '>':
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return -1
}

func stripXMLDeclarations(text string) (string, int) {
	var out strings.Builder
	removed := 0
	for i := 0; i < len(text); {
		if hasASCIIPrefixFold(text, i, "<![CDATA[") {
			end := strings.Index(text[i+9:], "]]>")
			if end < 0 {
				out.WriteString(text[i:])
				break
			}
			end += i + 9 + 3
			out.WriteString(text[i:end])
			i = end
			continue
		}
		if strings.HasPrefix(text[i:], "<!--") {
			end := strings.Index(text[i+4:], "-->")
			if end < 0 {
				out.WriteString(text[i:])
				break
			}
			end += i + 4 + 3
			out.WriteString(text[i:end])
			i = end
			continue
		}
		if keyword := xmlDeclarationKeyword(text, i); keyword != "" {
			if end := xmlDeclarationEnd(text, i, keyword); end >= 0 {
				removed++
				i = end
				continue
			}
			// Once a declaration is unterminated, the rest is ambiguous XML
			// declaration content. Preserve it verbatim and stop so a flood of
			// malformed declarations cannot trigger repeated full-tail scans.
			out.WriteString(text[i:])
			break
		}
		out.WriteByte(text[i])
		if text[i] == '<' {
			end, closed := scanHTMLTagEnd(text, i)
			if closed && end > i+1 {
				out.WriteString(text[i+1 : end])
				i = end
				continue
			}
			// The remainder is an unterminated tag; keep it as-is and avoid
			// restarting a full scan at every later '<' byte.
			out.WriteString(text[i+1:])
			break
		}
		i++
	}
	return out.String(), removed
}

func svgRootStart(text string) (int, int) {
	lower := strings.ToLower(text)
	for pos := 0; pos < len(text); {
		if strings.HasPrefix(lower[pos:], "<![cdata[") {
			end := strings.Index(lower[pos+9:], "]]>")
			if end < 0 {
				return -1, -1
			}
			pos += 9 + end + 3
			continue
		}
		if strings.HasPrefix(text[pos:], "<!--") {
			end := strings.Index(text[pos+4:], "-->")
			if end < 0 {
				return -1, -1
			}
			pos += 4 + end + 3
			continue
		}
		if strings.HasPrefix(text[pos:], "<?") {
			end := strings.Index(text[pos+2:], "?>")
			if end < 0 {
				return -1, -1
			}
			pos += 2 + end + 2
			continue
		}
		start := strings.Index(lower[pos:], "<svg")
		if start < 0 {
			return -1, -1
		}
		start += pos
		boundary := start + len("<svg")
		if boundary >= len(text) || isXMLNameByte(text[boundary]) || text[boundary] == '-' || text[boundary] == '.' {
			pos = boundary
			continue
		}
		end, closed := scanHTMLTagEnd(text, start)
		if !closed {
			return -1, -1
		}
		return start, end
	}
	return -1, -1
}

func cleanSVGRootAttributes(tag string) (string, int) {
	return cleanTagAttributes(tag, func(name string) bool {
		name = strings.ToLower(name)
		return name == "generator" || name == "creator" || name == "software" ||
			name == "inkscape:version" || name == "sodipodi:docname" ||
			name == "c2pa" || name == "provenance" || strings.HasPrefix(name, "ai-generated")
	})
}

func cleanTagAttributes(tag string, shouldDrop func(string) bool) (string, int) {
	if len(tag) < 3 || tag[0] != '<' || tag[1] == '/' || tag[1] == '!' || tag[1] == '?' {
		return tag, 0
	}
	i := 1
	for i < len(tag) && !isHTMLSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	if i <= 1 {
		return tag, 0
	}
	var out strings.Builder
	out.WriteString(tag[:i])
	removed := 0
	for i < len(tag) {
		attrStart := i
		for i < len(tag) && isHTMLSpace(tag[i]) {
			i++
		}
		if i >= len(tag) {
			out.WriteString(tag[attrStart:])
			break
		}
		if tag[i] == '>' || (tag[i] == '/' && i+1 < len(tag) && tag[i+1] == '>') {
			out.WriteString(tag[attrStart:])
			break
		}
		nameStart := i
		for i < len(tag) && htmlAttrNameChar(tag[i]) {
			i++
		}
		if nameStart == i {
			out.WriteString(tag[attrStart : i+1])
			i++
			continue
		}
		name := tag[nameStart:i]
		for i < len(tag) && isHTMLSpace(tag[i]) {
			i++
		}
		hasValue := false
		if i < len(tag) && tag[i] == '=' {
			hasValue = true
			i++
			for i < len(tag) && isHTMLSpace(tag[i]) {
				i++
			}
			if i < len(tag) && (tag[i] == '\'' || tag[i] == '"') {
				quote := tag[i]
				i++
				for i < len(tag) && tag[i] != quote {
					i++
				}
				if i < len(tag) {
					i++
				}
			} else {
				for i < len(tag) && !isHTMLSpace(tag[i]) && tag[i] != '>' {
					i++
				}
			}
		}
		if hasValue && shouldDrop(name) {
			removed++
			continue
		}
		out.WriteString(tag[attrStart:i])
	}
	return out.String(), removed
}

func inspectHTML(data []byte) (bool, bool, []string, map[string]any) {
	text := string(data)
	findings := []string{}
	var c2pa, ai bool
	for _, block := range htmlMetaTags(text) {
		tag := text[block.openStart:block.openEnd]
		lowerTag := strings.ToLower(tag)
		if strings.Contains(lowerTag, "c2pa") || strings.Contains(lowerTag, "contentcredential") ||
			strings.Contains(lowerTag, "content-credential") || strings.Contains(lowerTag, "content credential") {
			c2pa = true
		}
		if isCMSGenerator(tag) {
			findings = append(findings, "info: cms generator: "+short(tag))
			continue
		}
		if htmlMetaAI(tag) {
			ai = true
			findings = append(findings, "meta: "+short(tag))
		}
	}
	for _, block := range htmlScriptBlocks(text) {
		if !scriptTagIsJSONLD(text[block.openStart:block.openEnd]) {
			continue
		}
		raw := text[block.openStart:block.closeEnd]
		if aiMetaNameRE.MatchString(raw) || regexp.MustCompile(`(?i)DigitalSourceType|trainedAlgorithmicMedia|SoftwareAgent`).MatchString(raw) {
			ai = true
			if strings.Contains(strings.ToLower(raw), "c2pa") || strings.Contains(strings.ToLower(raw), "contentcredential") {
				c2pa = true
			}
			findings = append(findings, "json-ld provenance-like block")
		}
	}
	for _, attr := range dataAIInspectAttrRE.FindAllString(text, -1) {
		ai = true
		findings = append(findings, "attr: "+short(attr))
	}
	c2, a, more := inspectEmbeddedDataURIs(text)
	c2pa = c2pa || c2
	ai = ai || a
	findings = append(findings, more...)
	decoded := html.UnescapeString(text)
	if decoded != text {
		if h := containsAny([]byte(decoded), append(aiMetaHints, c2paMarkers...)); len(h) > 0 {
			ai = true
			if isC2PAHit(h) {
				c2pa = true
			}
			findings = append(findings, "entity-encoded provenance markers: "+strings.Join(h, ", "))
		}
	}
	return c2pa, ai || c2pa, findings, map[string]any{}
}

func htmlMetaAI(tag string) bool {
	if aiMetaNameRE.MatchString(tag) {
		return true
	}
	lower := strings.ToLower(tag)
	for _, hint := range aiMetaHints {
		if bytes.Contains([]byte(lower), bytes.ToLower(hint)) {
			return true
		}
	}
	return false
}

func cleanHTML(data []byte, opts Options) ([]byte, []string, error) {
	text := string(data)
	actions := []string{}
	if blocks := htmlMetaTags(text); len(blocks) > 0 {
		var out strings.Builder
		last := 0
		for _, block := range blocks {
			tag := text[block.openStart:block.openEnd]
			out.WriteString(text[last:block.openStart])
			if isCMSGenerator(tag) || !htmlCleanMetaRE.MatchString(tag) {
				out.WriteString(tag)
			} else {
				actions = append(actions, "drop meta: "+short(tag))
			}
			last = block.openEnd
		}
		out.WriteString(text[last:])
		text = out.String()
	}
	if blocks := htmlScriptBlocks(text); len(blocks) > 0 {
		var out strings.Builder
		last := 0
		removed := 0
		for _, block := range blocks {
			raw := text[block.openStart:block.closeEnd]
			if !scriptTagIsJSONLD(text[block.openStart:block.openEnd]) ||
				(!aiMetaNameRE.MatchString(raw) && !regexp.MustCompile(`(?i)DigitalSourceType|trainedAlgorithmicMedia|SoftwareAgent`).MatchString(raw)) {
				continue
			}
			out.WriteString(text[last:block.openStart])
			last = block.closeEnd
			removed++
			actions = append(actions, "drop json-ld provenance-like script")
		}
		if removed > 0 {
			out.WriteString(text[last:])
			text = out.String()
		}
	}
	var attrActions int
	text, attrActions = cleanHTMLDataAIAttributes(text)
	if attrActions > 0 {
		actions = append(actions, fmt.Sprintf("drop data-ai* attributes x%d", attrActions))
	}
	var uriActions []string
	text, uriActions = cleanEmbeddedDataURIs(text, opts)
	actions = append(actions, uriActions...)
	if len(actions) == 0 {
		actions = append(actions, "no HTML AI meta removed")
	}
	return []byte(text), actions, nil
}

func inspectSVG(data []byte) (bool, bool, []string, map[string]any) {
	text := string(data)
	c2pa, ai, hits := blobMarkerHits(data)
	findings := []string{}
	findings = append(findings, hits...)
	if len(xmlNamedBlocks(text, "metadata")) > 0 {
		ai = true
		findings = append(findings, "svg <metadata> present")
	}
	if regexp.MustCompile(`(?i)xmpmeta|rdf:RDF|contentcredentials`).MatchString(text) {
		ai = true
		findings = append(findings, "XMP/RDF-like content in SVG")
	}
	c2, a, more := inspectEmbeddedDataURIs(text)
	c2pa = c2pa || c2
	ai = ai || a
	findings = append(findings, more...)
	return c2pa, ai || c2pa, findings, map[string]any{}
}

func cleanSVG(data []byte, opts Options) ([]byte, []string, error) {
	text := string(data)
	actions := []string{}
	var n int
	text, n = dropTextBlocks(text, xmlNamedBlocks(text, "metadata"), func(string) bool { return true })
	if n > 0 {
		actions = append(actions, fmt.Sprintf("drop <metadata> x%d", n))
	}
	text, n = dropTextBlocks(text, xmlNamedBlocks(text, "x:xmpmeta"), func(string) bool { return true })
	if n > 0 {
		actions = append(actions, fmt.Sprintf("drop xmpmeta x%d", n))
	}
	var declarations int
	text, declarations = stripXMLDeclarations(text)
	if declarations > 0 {
		actions = append(actions, fmt.Sprintf("drop DOCTYPE/entity declarations x%d", declarations))
	}
	if blocks := commentBlocks(text); len(blocks) > 0 {
		var removed int
		text, removed = dropTextBlocks(text, blocks, func(block string) bool {
			return aiMetaNameRE.MatchString(block)
		})
		if removed > 0 {
			actions = append(actions, fmt.Sprintf("drop SVG comment with AI markers x%d", removed))
		}
	}
	var uriActions []string
	text, uriActions = cleanEmbeddedDataURIs(text, opts)
	actions = append(actions, uriActions...)
	if start, end := svgRootStart(text); start >= 0 {
		if tag, removed := cleanSVGRootAttributes(text[start:end]); removed > 0 {
			text = text[:start] + tag + text[end:]
			actions = append(actions, fmt.Sprintf("drop generator-like attrs x%d", removed))
		}
	}
	if len(actions) == 0 {
		actions = append(actions, "no SVG metadata removed")
	}
	return []byte(text), actions, nil
}

func inspectPDF(data []byte, paths ...string) (bool, bool, []string, map[string]any) {
	path := ""
	if len(paths) > 0 {
		path = paths[0]
	}
	structured := pdfStructuredBlob(data)
	c2pa, ai, hits := blobMarkerHits(structured)
	findings := []string{}
	for _, hit := range hits {
		findings = append(findings, "pdf-structured:"+hit)
	}
	if packets := pdfXMPPackets(data); len(packets) > 0 {
		findings = append(findings, "XMP packet present")
		xmp := make([]byte, 0)
		for _, packet := range packets {
			if !validPDFByteBlock(packet, len(data)) {
				continue
			}
			xmp = append(xmp, data[packet.openStart:packet.closeEnd]...)
		}
		lower := bytes.ToLower(xmp)
		for _, marker := range [][]byte{
			[]byte("digitalsourcetype"), []byte("trainedalgorithmicmedia"),
			[]byte("softwareagent"), []byte("c2pa"),
		} {
			if bytes.Contains(lower, marker) {
				ai = true
				break
			}
		}
	}
	if len(findings) == 0 {
		findings = []string{"no PDF AI metadata markers found"}
	}
	tools := map[string]any{}
	if path != "" {
		tools = inspectOptionalTools(path, data)
	}
	if c2paTool, ok := tools["c2patool"].(map[string]any); ok {
		if hasManifest, ok := c2paTool["has_manifest"].(bool); ok && hasManifest {
			c2pa = true
			ai = true
			findings = append(findings, "c2patool reports a C2PA-related manifest")
		}
		if available, ok := c2paTool["available"].(bool); ok && !available {
			findings = append(findings, "c2patool unavailable (not found); C2PA not fully inspected by this tool")
		} else if usable, ok := c2paTool["ok"].(bool); ok && !usable {
			detail, _ := c2paTool["error"].(string)
			if detail == "" {
				detail = "no usable verdict"
			}
			findings = append(findings, "c2patool probe inconclusive ("+detail+"); C2PA not fully inspected by this tool")
		}
	}
	return c2pa, ai || c2pa, findings, map[string]any{"tools": tools}
}

type byteBlock struct {
	openStart, openEnd   int
	closeStart, closeEnd int
}

func pdfStreamBlocks(data []byte) []byteBlock {
	blocks := []byteBlock{}
	for pos := 0; pos < len(data); {
		start := bytes.Index(data[pos:], []byte("stream"))
		if start < 0 {
			break
		}
		start += pos
		if (start > 0 && !isPDFWhitespace(data[start-1])) || start+6 >= len(data) {
			pos = start + 6
			continue
		}
		openEnd := start + 6
		if data[openEnd] == '\r' {
			if openEnd+1 >= len(data) || data[openEnd+1] != '\n' {
				pos = openEnd
				continue
			}
			openEnd += 2
		} else if data[openEnd] == '\n' {
			openEnd++
		} else {
			pos = openEnd
			continue
		}
		closeStart := bytes.Index(data[openEnd:], []byte("endstream"))
		if closeStart < 0 {
			break
		}
		closeStart += openEnd
		if closeStart > openEnd && !isPDFWhitespace(data[closeStart-1]) {
			pos = closeStart + len("endstream")
			continue
		}
		blocks = append(blocks, byteBlock{openStart: start, openEnd: openEnd, closeStart: closeStart, closeEnd: closeStart + len("endstream")})
		pos = closeStart + len("endstream")
	}
	return blocks
}

func isPDFWhitespace(value byte) bool {
	return value == 0 || value == '\t' || value == '\n' || value == '\f' || value == '\r' || value == ' '
}

func pdfXMPPackets(data []byte) []byteBlock {
	// Use ASCII-only folding here. bytes.ToLower applies Unicode mappings,
	// which can change the byte length (for example U+023A -> U+2C65). The
	// indexes returned by bytes.Index are later applied to the original PDF;
	// a length-changing fold would therefore make malformed PDFs panic.
	lower := pdfASCIILower(data)
	blocks := []byteBlock{}
	for pos := 0; pos < len(data); {
		start := bytes.Index(lower[pos:], []byte("<?xpacket begin"))
		if start < 0 {
			break
		}
		start += pos
		closeStart := bytes.Index(lower[start+len("<?xpacket begin"):], []byte("<?xpacket end"))
		if closeStart < 0 {
			break
		}
		closeStart += start + len("<?xpacket begin")
		closeEndRel := bytes.Index(lower[closeStart:], []byte("?>"))
		if closeEndRel < 0 {
			break
		}
		closeEnd := closeStart + closeEndRel + 2
		blocks = append(blocks, byteBlock{openStart: start, openEnd: start + len("<?xpacket begin"), closeStart: closeStart, closeEnd: closeEnd})
		pos = closeEnd
	}
	return blocks
}

func pdfASCIILower(data []byte) []byte {
	lower := append([]byte(nil), data...)
	for i, value := range lower {
		if value >= 'A' && value <= 'Z' {
			lower[i] = value + ('a' - 'A')
		}
	}
	return lower
}

func validPDFByteBlock(block byteBlock, size int) bool {
	return block.openStart >= 0 && block.openStart <= block.openEnd &&
		block.openEnd <= block.closeStart && block.closeStart <= block.closeEnd &&
		block.closeEnd <= size
}

func pdfStructuredBlob(data []byte) []byte {
	blocks := pdfStreamBlocks(data)
	var out bytes.Buffer
	last := 0
	for _, block := range blocks {
		out.Write(data[last:block.openStart])
		out.WriteString("stream endstream")
		last = block.closeEnd
	}
	if len(blocks) == 0 {
		out.Write(data)
	} else {
		out.Write(data[last:])
	}
	// Keep a separator even when no XMP packets exist. Besides matching the
	// upstream structured-blob contract, this prevents an unterminated stream
	// marker flood from being accidentally concatenated with later evidence.
	out.WriteByte('\n')
	for _, block := range pdfXMPPackets(data) {
		if !validPDFByteBlock(block, len(data)) {
			continue
		}
		out.Write(data[block.openStart:block.closeEnd])
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func cleanPDFFallback(data []byte, opts Options) ([]byte, []string, error) {
	out := append([]byte(nil), data...)
	actions := []string{}
	stripAll := stripAllMetadata(opts)
	if packets := pdfXMPPackets(out); len(packets) > 0 {
		blanked := 0
		for _, packet := range packets {
			if !validPDFByteBlock(packet, len(out)) {
				continue
			}
			if !stripAll && len(containsAny(out[packet.openStart:packet.closeEnd], aiMetaHints)) == 0 {
				continue
			}
			blanked++
			for i := packet.openStart; i < packet.closeEnd; i++ {
				if out[i] != '\n' && out[i] != '\r' && out[i] != '\t' {
					out[i] = ' '
				}
			}
		}
		if blanked > 0 {
			actions = append(actions, fmt.Sprintf("blanked PDF XMP packet x%d", blanked))
		}
	}
	if stripAll {
		// Info-dictionary fields are outside stream payloads. Never rewrite a
		// coincidental /Author (...) or /Title (...) sequence in compressed or
		// otherwise opaque page data.
		matches := pdfFieldRE.FindAllIndex(out, -1)
		streams := pdfStreamBlocks(out)
		var rewritten bytes.Buffer
		last := 0
		for _, match := range matches {
			if pdfRangeInsideStream(match[0], match[1], streams) {
				continue
			}
			rewritten.Write(out[last:match[0]])
			value := out[match[0]:match[1]]
			open := bytes.IndexByte(value, '(')
			if open < 0 {
				rewritten.Write(value)
			} else {
				rewritten.Write(value[:open+1])
				rewritten.WriteByte(')')
				actions = append(actions, "blanked PDF metadata field")
			}
			last = match[1]
		}
		if last > 0 {
			rewritten.Write(out[last:])
			out = rewritten.Bytes()
		}
	}
	if len(actions) == 0 {
		actions = append(actions, "no PDF metadata removed by the stdlib fallback")
	}
	return out, actions, nil
}

func pdfRangeInsideStream(start, end int, streams []byteBlock) bool {
	for _, stream := range streams {
		if start >= stream.openEnd && end <= stream.closeStart {
			return true
		}
	}
	return false
}

func inspectZipContainer(data []byte, format string, options ...Options) (bool, bool, []string, map[string]any) {
	c2pa, ai, findings, details, _ := inspectZipContainerWithBudget(data, format, nil, options...)
	return c2pa, ai, findings, details
}

func inspectZipContainerWithBudget(data []byte, format string, sharedBudget *int64, options ...Options) (bool, bool, []string, map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, false, []string{"not a valid " + strings.ToUpper(format) + " zip"}, nil, nil
	}
	opts := DefaultOptions()
	if len(options) > 0 {
		opts = options[0]
	}
	findings := []string{}
	var c2pa, ai bool
	budget := sharedBudget
	if budget == nil {
		budget = new(int64)
	}
	partialReason := ""
	customXML := 0
	encrypted := map[string]bool{}
	if format == "epub" {
		encrypted = encryptedEPUBMembers(data)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if e := checkZipMemberDeclaredSize(f); e != nil {
			return c2pa, ai || c2pa, findings, map[string]any{"parts": len(zr.File)}, e
		}
		originalName := f.Name
		name := strings.ToLower(originalName)
		if format == "docx" || format == "xlsx" || format == "pptx" {
			needsRead := isOOXMLMediaName(name) || strings.HasPrefix(name, "docprops/") || strings.HasPrefix(name, "customxml/")
			if !needsRead {
				continue
			}
		}
		raw, e := readZipMemberBounded(f, budget)
		if isZipBudgetExceeded(e) {
			return c2pa, ai || c2pa, findings, map[string]any{"parts": len(zr.File)}, e
		}
		if e != nil {
			partialReason = fmt.Sprintf("member %s could not be read: %v", f.Name, e)
			break
		}
		if encrypted[f.Name] || encrypted[name] {
			findings = append(findings, f.Name+": encrypted content (skipped)")
			continue
		}
		switch {
		case (format == "docx" || format == "xlsx" || format == "pptx") && isOOXMLMediaName(name):
			if imgFmt := embeddedImageFormat(raw, f.Name); imgFmt != "unknown" {
				sub := inspectEmbeddedImage(raw, f.Name, opts)
				if sub.HasC2PA {
					c2pa = true
				}
				if sub.HasAIMetadata {
					ai = true
				}
				for _, item := range sub.Findings {
					findings = append(findings, f.Name+": "+item)
				}
			}
		case (format == "docx" || format == "xlsx" || format == "pptx") &&
			(strings.HasPrefix(originalName, "docProps/") || strings.HasPrefix(originalName, "customXml/")):
			if strings.HasPrefix(originalName, "customXml/") {
				customXML++
			}
			appendZipMarkerFinding(&c2pa, &ai, &findings, f.Name, raw)
		case format == "odt":
			if imgFmt := embeddedImageFormat(raw, f.Name); imgFmt != "unknown" {
				sub := inspectEmbeddedImage(raw, f.Name, opts)
				c2pa = c2pa || sub.HasC2PA
				ai = ai || sub.HasAIMetadata
				for _, item := range sub.Findings {
					findings = append(findings, f.Name+": "+item)
				}
			} else {
				appendZipMarkerFinding(&c2pa, &ai, &findings, f.Name, raw)
			}
			if strings.EqualFold(f.Name, "meta.xml") && aiMetaNameRE.Match(raw) {
				ai = true
				findings = append(findings, "meta.xml generator-like fields")
			}
		case format == "epub" && isEPUBHTMLName(name):
			c, a, h, _ := inspectHTML(raw)
			c2pa = c2pa || c
			ai = ai || a
			for _, item := range h {
				findings = append(findings, f.Name+": "+item)
			}
		case format == "epub" && strings.HasSuffix(name, ".opf"):
			if aiMetaNameRE.Match(raw) {
				ai = true
				findings = append(findings, f.Name+": AI-ish metadata in package document")
			}
			appendZipMarkerFinding(&c2pa, &ai, &findings, f.Name, raw)
		case format == "epub":
			appendZipMarkerFinding(&c2pa, &ai, &findings, f.Name, raw)
		}
	}
	if partialReason != "" {
		if len(findings) == 0 {
			return false, false, []string{"not a valid " + strings.ToUpper(format) + " zip"}, nil, nil
		}
		findings = append(findings, fmt.Sprintf("partial read of %s zip (%s); evidence above survives, later members were not scanned", strings.ToUpper(format), partialReason))
	}
	if customXML > 0 {
		findings = append(findings, fmt.Sprintf("customXml parts: %d", customXML))
	}
	return c2pa, ai || c2pa, findings, map[string]any{"parts": len(zr.File)}, nil
}

func appendZipMarkerFinding(c2pa, ai *bool, findings *[]string, name string, raw []byte) {
	hasC2PA, hasAI, hits := blobMarkerHits(raw)
	if !hasC2PA && !hasAI {
		return
	}
	*ai = *ai || hasAI
	if hasC2PA {
		*c2pa = true
	}
	*findings = append(*findings, name+": "+strings.Join(hits[:minInt(6, len(hits))], ", "))
}

type zipMember struct {
	header zip.FileHeader
	name   string
	data   []byte
}

func cleanZipContainer(data []byte, format string, opts Options) ([]byte, []string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return data, nil, err
	}
	actions := []string{}
	budget := int64(0)
	encrypted := map[string]bool{}
	if format == "epub" {
		encrypted = encryptedEPUBMembers(data)
	}
	dropped := map[string]bool{}
	kept := []zipMember{}
	layerRemoved, layerReplaced := 0, 0
	contentTypeActionMarker := "\x00aiwr-content-types"
	contentTypeResults := [][]string{}
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			kept = append(kept, zipMember{header: f.FileHeader, name: name})
			continue
		}
		if e := checkZipMemberDeclaredSize(f); e != nil {
			return data, nil, e
		}
		raw, e := readZipMemberBounded(f, &budget)
		if isZipBudgetExceeded(e) {
			return data, nil, e
		}
		if e != nil {
			return data, nil, e
		}
		low := strings.ToLower(name)
		if encrypted[name] || encrypted[low] {
			// Encrypted EPUB parts are opaque ciphertext. Rewriting them as
			// UTF-8/XML would corrupt the publication, so carry their bytes
			// and original header through untouched.
			kept = append(kept, zipMember{header: f.FileHeader, name: name, data: raw})
			continue
		}
		if strings.EqualFold(name, "[Content_Types].xml") &&
			(format == "docx" || format == "xlsx" || format == "pptx") {
			// Content_Types is rewritten after the set of dropped parts is known,
			// but its action belongs at this member's position in the archive's
			// action stream, just as in the upstream cleaner.
			actions = append(actions, contentTypeActionMarker)
		}
		if (format == "docx" || format == "xlsx" || format == "pptx") &&
			(strings.HasPrefix(name, "customXml/") || name == "docProps/custom.xml") {
			dropped[name] = true
			actions = append(actions, "drop part "+name)
			continue
		}
		if format == "odt" && isODTDropCandidate(low, raw) {
			dropped[name] = true
			actions = append(actions, "drop part "+name+" (AI/C2PA markers)")
			continue
		}
		if format == "epub" && isEPUBDropCandidate(name, low, raw) {
			dropped[name] = true
			actions = append(actions, "drop part "+name+" (AI/C2PA markers)")
			continue
		}
		cleaned := raw
		var memberActions []string
		if format == "docx" || format == "xlsx" || format == "pptx" {
			if strings.HasPrefix(name, "docProps/") && strings.HasSuffix(name, ".xml") {
				cleaned, memberActions = scrubOOXML(raw, name)
			}
			if isOOXMLMediaName(low) {
				if imgFmt := embeddedImageFormat(raw, name); imgFmt != "unknown" {
					imageCleaned, imageActions, imageErr := cleanEmbeddedImage(raw, name, imgFmt, opts)
					if imageErr == nil {
						var accepted []string
						cleaned, accepted = acceptEmbeddedImageClean(raw, imageCleaned, name, imageActions)
						memberActions = append(memberActions, accepted...)
					}
				}
			}
			if opts.AlsoLayerAText && isOOXMLBodyXML(format, name) {
				var removed, replaced int
				cleaned, memberActions, removed, replaced = cleanTextXML(cleaned, opts, memberActions)
				layerRemoved += removed
				layerReplaced += replaced
			}
		} else if format == "odt" {
			if imgFmt := embeddedImageFormat(raw, name); imgFmt != "unknown" {
				imageCleaned, imageActions, imageErr := cleanEmbeddedImage(raw, name, imgFmt, opts)
				if imageErr == nil {
					var accepted []string
					cleaned, accepted = acceptEmbeddedImageClean(raw, imageCleaned, name, imageActions)
					memberActions = append(memberActions, accepted...)
				}
			}
			if low == "meta.xml" {
				cleaned, memberActions = scrubODTMeta(raw)
			}
			if opts.AlsoLayerAText && low == "content.xml" {
				var removed, replaced int
				cleaned, memberActions, removed, replaced = cleanTextXML(cleaned, opts, memberActions)
				layerRemoved += removed
				layerReplaced += replaced
			}
		} else if format == "epub" {
			if strings.HasSuffix(low, ".opf") {
				cleaned, memberActions = scrubEPUBOPF(raw)
				memberActions = prefixContainerActions(name, memberActions)
			} else if strings.HasSuffix(low, ".xhtml") || strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".htm") {
				cleaned, memberActions, err = cleanHTML(raw, opts)
				if err != nil {
					return data, nil, err
				}
				if opts.AlsoLayerAText {
					var removed, replaced int
					cleaned, removed, replaced = cleanContainerTextLayerStats(cleaned, opts)
					layerRemoved += removed
					layerReplaced += replaced
				}
				memberActions = prefixContainerActions(name, memberActions)
			} else if imgFmt := embeddedImageFormat(raw, name); imgFmt != "unknown" {
				imageCleaned, imageActions, imageErr := cleanEmbeddedImage(raw, name, imgFmt, opts)
				if imageErr == nil {
					var accepted []string
					cleaned, accepted = acceptEmbeddedImageClean(raw, imageCleaned, name, imageActions)
					memberActions = append(memberActions, accepted...)
				}
			}
		}
		for _, action := range memberActions {
			if strings.HasPrefix(action, "no ") {
				continue
			}
			actions = append(actions, action)
		}
		header := f.FileHeader
		if format == "epub" && low == "mimetype" {
			header.Method = zip.Store
		}
		kept = append(kept, zipMember{header: header, name: name, data: cleaned})
	}

	keptNames := map[string]bool{}
	for _, member := range kept {
		keptNames[member.name] = true
	}
	for i := range kept {
		member := &kept[i]
		if strings.HasSuffix(strings.ToLower(member.name), ".rels") {
			if cleaned, n := pruneOOXMLRelationships(member.name, member.data, keptNames); n > 0 {
				member.data = cleaned
				actions = append(actions, fmt.Sprintf("prune dangling relationships x%d in %s", n, member.name))
			}
		}
		if strings.EqualFold(member.name, "[Content_Types].xml") &&
			(format == "docx" || format == "xlsx" || format == "pptx") {
			var contentTypeActions []string
			if cleaned, customXML, customProperties := pruneOOXMLContentTypes(member.data, keptNames); customXML > 0 || customProperties > 0 {
				member.data = cleaned
				if customXML > 0 {
					contentTypeActions = append(contentTypeActions, fmt.Sprintf("drop Content_Types customXml overrides x%d", customXML))
				}
				if customProperties > 0 {
					contentTypeActions = append(contentTypeActions, fmt.Sprintf("drop Content_Types custom.xml override x%d", customProperties))
				}
			}
			contentTypeResults = append(contentTypeResults, contentTypeActions)
		}
		if format == "odt" && strings.EqualFold(member.name, "META-INF/manifest.xml") && len(dropped) > 0 {
			if cleaned, n := pruneODTManifest(member.data, dropped); n > 0 {
				member.data = cleaned
				actions = append(actions, fmt.Sprintf("drop manifest entries x%d", n))
			}
		}
		if format == "epub" && strings.HasSuffix(strings.ToLower(member.name), ".opf") && len(dropped) > 0 {
			if cleaned, n := pruneEPUBManifest(member.name, member.data, dropped); n > 0 {
				member.data = cleaned
				actions = append(actions, fmt.Sprintf("prune OPF manifest entries x%d", n))
			}
		}
	}
	if len(contentTypeResults) > 0 {
		ordered := make([]string, 0, len(actions))
		contentTypeIndex := 0
		for _, action := range actions {
			if action != contentTypeActionMarker {
				ordered = append(ordered, action)
				continue
			}
			if contentTypeIndex < len(contentTypeResults) {
				ordered = append(ordered, contentTypeResults[contentTypeIndex]...)
			}
			contentTypeIndex++
		}
		actions = ordered
	}
	if layerRemoved > 0 || layerReplaced > 0 {
		actions = append(actions, fmt.Sprintf("layer A text: removed=%d replaced=%d", layerRemoved, layerReplaced))
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, member := range kept {
		header := member.header
		w, e := zw.CreateHeader(&header)
		if e != nil {
			return data, nil, e
		}
		if len(member.data) > 0 {
			if _, e = w.Write(member.data); e != nil {
				return data, nil, e
			}
		}
	}
	if e := zw.Close(); e != nil {
		return data, nil, e
	}
	if len(actions) == 0 {
		label := strings.ToUpper(format)
		if format == "docx" || format == "xlsx" || format == "pptx" {
			actions = append(actions, "no "+label+" metadata parts removed")
		} else {
			actions = append(actions, "no "+label+" metadata removed")
		}
	}
	return buf.Bytes(), actions, nil
}

func prefixContainerActions(name string, actions []string) []string {
	if len(actions) == 0 {
		return nil
	}
	prefixed := make([]string, 0, len(actions))
	for _, action := range actions {
		if strings.HasPrefix(action, "no ") {
			prefixed = append(prefixed, action)
			continue
		}
		prefixed = append(prefixed, name+": "+action)
	}
	return prefixed
}

func encryptedEPUBMembers(data []byte) map[string]bool {
	out := map[string]bool{}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return out
	}
	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, "META-INF/encryption.xml") {
			continue
		}
		raw, readErr := readZipMemberBounded(f, nil)
		if readErr != nil {
			return out
		}
		re := regexp.MustCompile(`(?is)<(?:[A-Za-z0-9_.-]+:)?CipherReference\b[^>]*\bURI\s*=\s*["']([^"']+)["']`)
		for _, match := range re.FindAllStringSubmatch(string(raw), -1) {
			if len(match) < 2 {
				continue
			}
			uri := strings.ReplaceAll(match[1], "\\", "/")
			out[path.Clean(path.Join("META-INF", uri))] = true
			out[path.Clean(uri)] = true
			if strings.HasPrefix(uri, "../") {
				out[path.Clean(strings.TrimPrefix(uri, "../"))] = true
			}
		}
		break
	}
	return out
}

func isODTDropCandidate(name string, raw []byte) bool {
	if name == "content.xml" || name == "styles.xml" || name == "meta.xml" ||
		name == "mimetype" || name == "meta-inf/manifest.xml" {
		return false
	}
	if embeddedImageFormat(raw, name) != "unknown" {
		return false
	}
	c2, ai, _ := markerFlags(raw)
	return c2 || ai
}

func isEPUBContentPart(original, lower string, raw []byte) bool {
	if lower == "mimetype" || strings.HasSuffix(lower, ".rels") {
		return true
	}
	switch lower {
	case "meta-inf/container.xml", "meta-inf/encryption.xml", "meta-inf/signatures.xml", "meta-inf/rights.xml":
		return true
	}
	return epubContentRE.MatchString(lower)
}

func isEPUBDropCandidate(original, lower string, raw []byte) bool {
	if isEPUBContentPart(original, lower, raw) {
		return false
	}
	c2, ai, _ := markerFlags(raw)
	return c2 || ai
}

func markerFlags(raw []byte) (bool, bool, []string) {
	hits := containsAny(raw, append(append([][]byte(nil), aiMetaHints...), c2paMarkers...))
	return isC2PAHit(hits), len(hits) > 0, hits
}

func pruneODTManifest(raw []byte, dropped map[string]bool) ([]byte, int) {
	text := string(raw)
	blocks := xmlOpenTags(text, func(name string) bool {
		local := name
		if colon := strings.LastIndexByte(local, ':'); colon >= 0 {
			local = local[colon+1:]
		}
		return strings.EqualFold(local, "file-entry")
	})
	out, removed := dropTextBlocks(text, blocks, func(tag string) bool {
		fullPath := xmlAttribute(tag, "full-path")
		if fullPath == "" || fullPath == "/" {
			return false
		}
		return dropped[fullPath] || dropped[strings.TrimPrefix(fullPath, "/")]
	})
	return []byte(out), removed
}

func pruneEPUBManifest(name string, raw []byte, dropped map[string]bool) ([]byte, int) {
	text := string(raw)
	base := path.Dir(strings.ReplaceAll(name, "\\", "/"))
	removedIDs := map[string]bool{}
	blocks := xmlNamedOpenTags(text, "item")
	out, removed := dropTextBlocks(text, blocks, func(tag string) bool {
		href := strings.TrimSpace(xmlAttribute(tag, "href"))
		if href == "" {
			return false
		}
		href = strings.SplitN(href, "#", 2)[0]
		resolved := path.Clean(path.Join(base, strings.ReplaceAll(href, "\\", "/")))
		if dropped[resolved] || dropped[strings.TrimPrefix(resolved, "./")] {
			if id := strings.TrimSpace(xmlAttribute(tag, "id")); id != "" {
				removedIDs[id] = true
			}
			return true
		}
		return false
	})
	if len(removedIDs) > 0 {
		refs := xmlNamedOpenTags(out, "itemref")
		var refRemoved int
		out, refRemoved = dropTextBlocks(out, refs, func(tag string) bool {
			return removedIDs[strings.TrimSpace(xmlAttribute(tag, "idref"))]
		})
		removed += refRemoved
	}
	return []byte(out), removed
}

func isOOXMLMediaName(name string) bool {
	return strings.HasPrefix(name, "word/media/") || strings.HasPrefix(name, "xl/media/") || strings.HasPrefix(name, "ppt/media/")
}

func isOOXMLBodyXML(format, name string) bool {
	switch format {
	case "docx":
		return strings.HasPrefix(name, "word/") && strings.HasSuffix(name, ".xml")
	case "xlsx":
		return strings.HasPrefix(name, "xl/") && strings.HasSuffix(name, ".xml")
	case "pptx":
		return strings.HasPrefix(name, "ppt/") && strings.HasSuffix(name, ".xml")
	default:
		return false
	}
}

func xmlAttribute(tag, key string) string {
	lowerTag, lowerKey := strings.ToLower(tag), strings.ToLower(key)
	for start := 0; start+len(lowerKey) <= len(lowerTag); {
		offset := strings.Index(lowerTag[start:], lowerKey)
		if offset < 0 {
			return ""
		}
		offset += start
		// XML attributes may be namespace-qualified (for example,
		// manifest:full-path).  Match the local name after the colon while
		// still requiring an attribute-name boundary before it.
		beforeOK := offset == 0 || lowerTag[offset-1] == '<' || lowerTag[offset-1] == ':' || lowerTag[offset-1] == ' ' || lowerTag[offset-1] == '\t' || lowerTag[offset-1] == '\n' || lowerTag[offset-1] == '\r'
		end := offset + len(lowerKey)
		afterOK := end == len(lowerTag) || lowerTag[end] == ' ' || lowerTag[end] == '\t' || lowerTag[end] == '\n' || lowerTag[end] == '\r' || lowerTag[end] == '='
		if !beforeOK || !afterOK {
			start = end
			continue
		}
		for end < len(tag) && (tag[end] == ' ' || tag[end] == '\t' || tag[end] == '\n' || tag[end] == '\r') {
			end++
		}
		if end >= len(tag) || tag[end] != '=' {
			start = end
			continue
		}
		end++
		for end < len(tag) && (tag[end] == ' ' || tag[end] == '\t' || tag[end] == '\n' || tag[end] == '\r') {
			end++
		}
		if end >= len(tag) {
			return ""
		}
		quote := tag[end]
		if quote == '\'' || quote == '"' {
			valueStart := end + 1
			if valueEnd := strings.IndexByte(tag[valueStart:], quote); valueEnd >= 0 {
				return tag[valueStart : valueStart+valueEnd]
			}
			return ""
		}
		valueStart := end
		for end < len(tag) && tag[end] != ' ' && tag[end] != '\t' && tag[end] != '\n' && tag[end] != '\r' && tag[end] != '>' {
			end++
		}
		return tag[valueStart:end]
	}
	return ""
}

func pruneOOXMLRelationships(name string, raw []byte, keptNames map[string]bool) ([]byte, int) {
	text := string(raw)
	base := path.Dir(path.Dir(strings.ReplaceAll(name, "\\", "/")))
	blocks := xmlNamedOpenTags(text, "Relationship")
	out, removed := dropTextBlocks(text, blocks, func(tag string) bool {
		if xmlAttribute(tag, "TargetMode") != "" {
			return false
		}
		target := strings.ReplaceAll(xmlAttribute(tag, "Target"), "\\", "/")
		if target == "" || strings.HasPrefix(target, "#") {
			return false
		}
		resolved := path.Clean(strings.TrimPrefix(target, "/"))
		if !strings.HasPrefix(target, "/") {
			resolved = path.Clean(path.Join(base, target))
		}
		if keptNames[resolved] || resolved == "." {
			return false
		}
		return true
	})
	return []byte(out), removed
}

func pruneOOXMLContentTypes(raw []byte, keptNames map[string]bool) ([]byte, int, int) {
	text := string(raw)
	blocks := xmlNamedOpenTags(text, "Override")
	customXML, customProperties := 0, 0
	out, _ := dropTextBlocks(text, blocks, func(tag string) bool {
		part := strings.TrimPrefix(strings.ReplaceAll(xmlAttribute(tag, "PartName"), "\\", "/"), "/")
		if part == "" || keptNames[part] {
			return false
		}
		switch {
		case strings.HasPrefix(part, "customXml/"):
			customXML++
			return true
		case strings.EqualFold(part, "docProps/custom.xml"):
			customProperties++
			return true
		default:
			return false
		}
	})
	return []byte(out), customXML, customProperties
}

func cleanTextXML(raw []byte, opts Options, actions []string) ([]byte, []string, int, int) {
	text := string(raw)
	var cleaned string
	removed, replaced := 0, 0
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "<w:t"):
		cleaned, removed, replaced = rewriteXMLTextRuns(text, "w:t", opts)
	case strings.Contains(lower, "<a:t"):
		cleaned, removed, replaced = rewriteXMLTextRuns(text, "a:t", opts)
	case strings.Contains(lower, "<text:p"):
		cleaned, removed, replaced = rewriteODTTextRuns(text, opts)
	case strings.Contains(lower, "<t"):
		cleaned, removed, replaced = rewriteXMLTextRuns(text, "t", opts)
	default:
		if !strings.Contains(text, "<") {
			clean, stats := cleanText(raw, opts)
			removed, _ = stats["removed_count"].(int)
			replaced, _ = stats["replaced_count"].(int)
			if removed == 0 && replaced == 0 {
				return raw, actions, 0, 0
			}
			return clean, actions, removed, replaced
		}
		return raw, actions, 0, 0
	}
	return []byte(cleaned), actions, removed, replaced
}

func xmlEscapeText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	return strings.ReplaceAll(value, ">", "&gt;")
}

func rewriteXMLTextRuns(text, element string, opts Options) (string, int, int) {
	lower := strings.ToLower(text)
	openNeedle := "<" + element
	closeNeedle := "</" + element
	openLower, closeLower := strings.ToLower(openNeedle), strings.ToLower(closeNeedle)
	removed, replaced := 0, 0
	var out strings.Builder
	last, pos := 0, 0
	for pos < len(text) {
		start := strings.Index(lower[pos:], openLower)
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len(openNeedle)
		if boundary >= len(text) || !isXMLNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		openEnd := xmlTagEnd(text, start)
		if openEnd < 0 {
			break
		}
		closeStart := strings.Index(lower[openEnd:], closeLower)
		if closeStart < 0 {
			break
		}
		closeStart += openEnd
		if closeStart+len(closeNeedle) >= len(text) || !isXMLNameBoundary(text[closeStart+len(closeNeedle)]) {
			pos = closeStart + len(closeNeedle)
			continue
		}
		closeEnd := xmlTagEnd(text, closeStart)
		if closeEnd < 0 {
			break
		}
		inner := text[openEnd:closeStart]
		cleaned, stats := cleanText([]byte(decodeXMLEntities(inner)), opts)
		removedCount, _ := stats["removed_count"].(int)
		replacedCount, _ := stats["replaced_count"].(int)
		if removedCount > 0 || replacedCount > 0 {
			openTag := text[start:openEnd]
			if (len(cleaned) > 0 && (isXMLSpace(cleaned[0]) || isXMLSpace(cleaned[len(cleaned)-1]))) && !strings.Contains(strings.ToLower(openTag), "xml:space") {
				openTag = strings.TrimSuffix(openTag, ">") + ` xml:space="preserve">`
			}
			out.WriteString(text[last:start])
			out.WriteString(openTag)
			out.WriteString(xmlEscapeText(string(cleaned)))
			out.WriteString(text[closeStart:closeEnd])
			last = closeEnd
			removed += removedCount
			replaced += replacedCount
		}
		pos = closeEnd
	}
	if last == 0 {
		return text, 0, 0
	}
	out.WriteString(text[last:])
	return out.String(), removed, replaced
}

func isXMLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func rewriteODTTextRuns(text string, opts Options) (string, int, int) {
	lower := strings.ToLower(text)
	openNeedle, closeNeedle := "<text:p", "</text:p"
	removed, replaced := 0, 0
	var out strings.Builder
	last, pos := 0, 0
	for pos < len(text) {
		start := strings.Index(lower[pos:], openNeedle)
		if start < 0 {
			break
		}
		start += pos
		boundary := start + len(openNeedle)
		if boundary >= len(text) || !isXMLNameBoundary(text[boundary]) {
			pos = boundary
			continue
		}
		openEnd := xmlTagEnd(text, start)
		if openEnd < 0 {
			break
		}
		closeStart := strings.Index(lower[openEnd:], closeNeedle)
		if closeStart < 0 {
			break
		}
		closeStart += openEnd
		if closeStart+len(closeNeedle) >= len(text) || !isXMLNameBoundary(text[closeStart+len(closeNeedle)]) {
			pos = closeStart + len(closeNeedle)
			continue
		}
		closeEnd := xmlTagEnd(text, closeStart)
		if closeEnd < 0 {
			break
		}
		inner := text[openEnd:closeStart]
		cleanedInner, r, rp := rewriteODTParagraphInner(inner, opts)
		if r > 0 || rp > 0 {
			openTag := text[start:openEnd]
			if len(cleanedInner) > 0 && (isXMLSpace(cleanedInner[0]) || isXMLSpace(cleanedInner[len(cleanedInner)-1])) && !strings.Contains(strings.ToLower(openTag), "xml:space") {
				openTag = strings.TrimSuffix(openTag, ">") + ` xml:space="preserve">`
			}
			out.WriteString(text[last:start])
			out.WriteString(openTag)
			out.WriteString(cleanedInner)
			out.WriteString(text[closeStart:closeEnd])
			last = closeEnd
			removed += r
			replaced += rp
		}
		pos = closeEnd
	}
	if last == 0 {
		return text, 0, 0
	}
	out.WriteString(text[last:])
	return out.String(), removed, replaced
}

func rewriteODTParagraphInner(inner string, opts Options) (string, int, int) {
	var out strings.Builder
	removed, replaced := 0, 0
	for pos := 0; pos < len(inner); {
		if inner[pos] == '<' {
			end := xmlTagEnd(inner, pos)
			if end < 0 {
				out.WriteString(inner[pos:])
				break
			}
			out.WriteString(inner[pos:end])
			pos = end
			continue
		}
		next := strings.IndexByte(inner[pos:], '<')
		if next < 0 {
			next = len(inner) - pos
		}
		segment := inner[pos : pos+next]
		cleaned, stats := cleanText([]byte(decodeXMLEntities(segment)), opts)
		out.WriteString(xmlEscapeText(string(cleaned)))
		if n, ok := stats["removed_count"].(int); ok {
			removed += n
		}
		if n, ok := stats["replaced_count"].(int); ok {
			replaced += n
		}
		pos += next
	}
	return out.String(), removed, replaced
}

// xmlNamedBlocks finds complete XML elements without interpreting markup-like
// text inside comments, CDATA, processing instructions, or quoted attributes.
// It is deliberately a small lexical scanner: the metadata elements we drop
// are simple leaf/container nodes, and a bounded linear scan is safer here
// than repeatedly applying a broad lazy regex to attacker-controlled XML.
func xmlNamedBlocks(text, name string) []textBlock {
	if name == "" {
		return nil
	}
	lower := strings.ToLower(text)
	openNeedle := "<" + strings.ToLower(name)
	closeNeedle := "</" + strings.ToLower(name)
	blocks := []textBlock{}
	for pos := 0; pos < len(text); {
		lt := strings.IndexByte(text[pos:], '<')
		if lt < 0 {
			break
		}
		start := pos + lt
		if end, ok := skipXMLMarkup(text, start); !ok {
			break
		} else if end != start {
			if strings.HasPrefix(lower[start:], openNeedle) && start+len(openNeedle) < len(text) &&
				isXMLNameBoundary(text[start+len(openNeedle)]) && text[start+len(openNeedle)] != '/' {
				closeStart, closeEnd, found := findXMLClose(text, lower, end, closeNeedle)
				if found {
					blocks = append(blocks, textBlock{openStart: start, openEnd: end, closeStart: closeStart, closeEnd: closeEnd})
					pos = closeEnd
					continue
				}
				break
			}
			pos = end
		}
	}
	return blocks
}

func xmlNamedOpenTags(text, name string) []textBlock {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return nil
	}
	return xmlOpenTags(text, func(tagName string) bool {
		return strings.EqualFold(tagName, needle)
	})
}

func xmlOpenTags(text string, matches func(string) bool) []textBlock {
	if matches == nil {
		return nil
	}
	tags := []textBlock{}
	for pos := 0; pos < len(text); {
		lt := strings.IndexByte(text[pos:], '<')
		if lt < 0 {
			break
		}
		start := pos + lt
		end, ok := skipXMLMarkup(text, start)
		if !ok {
			break
		}
		if name := xmlOpeningElementName(text[start:end]); name != "" && matches(name) {
			tags = append(tags, textBlock{openStart: start, openEnd: end, closeStart: start, closeEnd: end})
		}
		pos = end
	}
	return tags
}

func skipXMLMarkup(text string, start int) (int, bool) {
	if start < 0 || start >= len(text) || text[start] != '<' {
		return start, false
	}
	switch {
	case hasASCIIPrefixFold(text, start, "<!--"):
		end := strings.Index(text[start+4:], "-->")
		if end < 0 {
			return len(text), false
		}
		return start + 4 + end + 3, true
	case hasASCIIPrefixFold(text, start, "<![CDATA["):
		end := strings.Index(text[start+9:], "]]>")
		if end < 0 {
			return len(text), false
		}
		return start + 9 + end + 3, true
	case hasASCIIPrefixFold(text, start, "<?"):
		end := strings.Index(text[start+2:], "?>")
		if end < 0 {
			return len(text), false
		}
		return start + 2 + end + 2, true
	case hasASCIIPrefixFold(text, start, "<!DOCTYPE"), hasASCIIPrefixFold(text, start, "<!ENTITY"):
		keyword := "doctype"
		if hasASCIIPrefixFold(text, start, "<!ENTITY") {
			keyword = "entity"
		}
		if end := xmlDeclarationEnd(text, start, keyword); end >= 0 {
			return end, true
		}
		return len(text), false
	default:
		return scanHTMLTagEnd(text, start)
	}
}

func hasASCIIPrefixFold(text string, start int, prefix string) bool {
	if start < 0 || len(text)-start < len(prefix) {
		return false
	}
	for i := range prefix {
		left, right := text[start+i], prefix[i]
		if left >= 'A' && left <= 'Z' {
			left += 'a' - 'A'
		}
		if right >= 'A' && right <= 'Z' {
			right += 'a' - 'A'
		}
		if left != right {
			return false
		}
	}
	return true
}

func findXMLClose(text, lower string, pos int, closeNeedle string) (int, int, bool) {
	for pos < len(text) {
		lt := strings.IndexByte(text[pos:], '<')
		if lt < 0 {
			return 0, 0, false
		}
		start := pos + lt
		if end, ok := skipXMLMarkup(text, start); !ok {
			return 0, 0, false
		} else if strings.HasPrefix(lower[start:], closeNeedle) {
			boundary := start + len(closeNeedle)
			if boundary < len(text) && isXMLNameBoundary(text[boundary]) {
				if strings.TrimSpace(text[boundary:end-1]) == "" {
					return start, end, true
				}
			}
			pos = end
			continue
		} else {
			pos = end
		}
	}
	return 0, 0, false
}

func scrubOOXML(raw []byte, partNames ...string) ([]byte, []string) {
	text := string(raw)
	actions := []string{}
	partName := "OOXML metadata"
	if len(partNames) > 0 && strings.TrimSpace(partNames[0]) != "" {
		partName = partNames[0]
	}
	fields := []string{"dc:creator", "cp:lastModifiedBy", "dc:description", "cp:keywords", "dc:subject", "cp:category", "Application", "Company", "Manager"}
	for _, field := range fields {
		blocks := xmlNamedBlocks(text, field)
		if len(blocks) == 0 {
			continue
		}
		text, _ = replaceTextBlocks(text, blocks, func(block string) (string, bool) {
			openEnd := xmlTagEnd(block, 0)
			if openEnd < 0 {
				return "", false
			}
			closeStart := strings.LastIndex(strings.ToLower(block), "</"+strings.ToLower(field))
			if closeStart < openEnd {
				return "", false
			}
			closeEnd := xmlTagEnd(block, closeStart)
			if closeEnd < 0 {
				return "", false
			}
			actions = append(actions, fmt.Sprintf("scrub %s field %s", partName, field))
			return block[:openEnd] + block[closeStart:closeEnd], true
		})
	}
	if len(actions) == 0 {
		actions = append(actions, "no OOXML metadata removed")
	}
	return []byte(text), actions
}

func xmlOpeningElementName(tag string) string {
	if len(tag) < 3 || tag[0] != '<' || tag[1] == '/' || tag[1] == '!' || tag[1] == '?' {
		return ""
	}
	end := 1
	for end < len(tag) && (isXMLNameByte(tag[end]) || tag[end] == '-' || tag[end] == '.') {
		end++
	}
	return tag[1:end]
}

func isXMLSelfClosingTag(tag string) bool {
	i := len(tag) - 2
	for i >= 0 && isHTMLSpace(tag[i]) {
		i--
	}
	return i >= 0 && tag[i] == '/'
}

func scrubEPUBOPF(raw []byte) ([]byte, []string) {
	text := string(raw)
	actions := []string{}

	// OPF permits both self-closing and block-form <meta> elements. Scan XML
	// lexically so markup-looking text in comments/CDATA cannot be mistaken for
	// metadata, and only remove a block when its content is also AI-like.
	if blocks := xmlNamedBlocks(text, "meta"); len(blocks) > 0 {
		var removed int
		text, removed = dropTextBlocks(text, blocks, func(block string) bool {
			if !aiMetaNameRE.MatchString(block) {
				return false
			}
			actions = append(actions, "drop OPF meta tag")
			return true
		})
		_ = removed
	}
	if tags := xmlNamedOpenTags(text, "meta"); len(tags) > 0 {
		text, _ = dropTextBlocks(text, tags, func(tag string) bool {
			if !isXMLSelfClosingTag(tag) || !aiMetaNameRE.MatchString(tag) {
				return false
			}
			actions = append(actions, "drop OPF meta tag")
			return true
		})
	}

	for _, field := range []string{
		"dc:creator", "dc:contributor", "dc:publisher",
		"dc:description", "dc:rights", "dc:source",
	} {
		blocks := xmlNamedBlocks(text, field)
		if len(blocks) == 0 {
			continue
		}
		text, _ = replaceTextBlocks(text, blocks, func(block string) (string, bool) {
			if !aiMetaNameRE.MatchString(block) {
				return "", false
			}
			openEnd := xmlTagEnd(block, 0)
			name := ""
			if openEnd > 0 {
				name = xmlOpeningElementName(block[:openEnd])
			}
			if name == "" {
				// The known field name is safe to use as a fallback when an
				// unusual but well-bounded opening tag cannot be recovered.
				name = field
			}
			actions = append(actions, "scrub "+name+" (AI vendor name)")
			return "<" + name + "/>", true
		})
	}
	if len(actions) == 0 {
		actions = append(actions, "no OPF metadata removed")
	}
	return []byte(text), actions
}

func scrubODTMeta(raw []byte) ([]byte, []string) {
	text := string(raw)
	actions := []string{}
	for _, tag := range []string{"meta:generator", "dc:creator"} {
		blocks := xmlNamedBlocks(text, tag)
		if len(blocks) > 0 {
			text, _ = dropTextBlocks(text, blocks, func(string) bool { return true })
			actions = append(actions, "drop "+tag)
		}
	}
	if len(actions) == 0 {
		actions = append(actions, "no ODT metadata removed")
	}
	return []byte(text), actions
}

const maxEmbeddedDataBytes = int64(64 << 20)

type dataURI struct {
	start, end int
	mime       string
	params     string
	payload    string
	base64     bool
}

func dataURIChar(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '+' || value == '/' || value == '=' || value == '%'
}

func dataURIMimeChar(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '+' || value == '-' || value == '.'
}

func dataURIBreak(value byte) bool {
	return value == '"' || value == '\'' || value == '<' || value == '>' || value == '(' || value == ')'
}

func skipDataURICandidate(text string, start int) int {
	for i := start; i < len(text); i++ {
		if dataURIBreak(text[i]) {
			if i == start {
				return start + 1
			}
			return i
		}
	}
	return len(text)
}

func parseDataURI(text string, start int) (dataURI, int, bool) {
	prefix := "data:image/"
	if start < 0 || start+len(prefix) > len(text) || !strings.EqualFold(text[start:start+len(prefix)], prefix) {
		return dataURI{}, start + 1, false
	}
	i := start + len(prefix)
	mimeStart := i
	for i < len(text) && dataURIMimeChar(text[i]) {
		i++
	}
	if i == mimeStart {
		return dataURI{}, skipDataURICandidate(text, start), false
	}
	paramsStart := i
	for i < len(text) && text[i] == ';' {
		i++
		for i < len(text) && !dataURIBreak(text[i]) && !isHTMLSpace(text[i]) && text[i] != ',' && text[i] != ';' {
			i++
		}
	}
	if i >= len(text) || text[i] != ',' {
		return dataURI{}, skipDataURICandidate(text, start), false
	}
	params := text[paramsStart:i]
	isBase64 := strings.Contains(strings.ToLower(params), "base64")
	i++
	payloadStart := i
	for i < len(text) && ((isBase64 && (dataURIChar(text[i]) || isHTMLSpace(text[i]))) || (!isBase64 && !dataURIBreak(text[i]))) {
		i++
	}
	if i == payloadStart {
		return dataURI{}, skipDataURICandidate(text, start), false
	}
	return dataURI{
		start: start, end: i, mime: text[mimeStart:paramsStart],
		params: params, payload: text[payloadStart:i],
		base64: isBase64,
	}, i, true
}

func iterDataURIs(text string) []dataURI {
	lower := strings.ToLower(text)
	uris := []dataURI{}
	for pos := 0; pos < len(text); {
		start := strings.Index(lower[pos:], "data:image/")
		if start < 0 {
			break
		}
		start += pos
		uri, next, ok := parseDataURI(text, start)
		if ok {
			uris = append(uris, uri)
		}
		if next <= start {
			next = start + 1
		}
		pos = next
	}
	return uris
}

func decodeDataURI(uri dataURI) ([]byte, error) {
	if uri.base64 {
		var compact strings.Builder
		compact.Grow(len(uri.payload))
		for i := 0; i < len(uri.payload); i++ {
			if !isHTMLSpace(uri.payload[i]) {
				compact.WriteByte(uri.payload[i])
			}
		}
		value := compact.String()
		if int64(len(value)) > maxEmbeddedDataBytes*2 {
			return nil, fmt.Errorf("embedded data URI exceeds cap")
		}
		if remainder := len(value) % 4; remainder != 0 {
			value += strings.Repeat("=", 4-remainder)
		}
		if int64(base64.StdEncoding.DecodedLen(len(value))) > maxEmbeddedDataBytes {
			return nil, fmt.Errorf("embedded data URI exceeds cap")
		}
		return base64.StdEncoding.DecodeString(value)
	}
	if int64(len(uri.payload)) > maxEmbeddedDataBytes {
		return nil, fmt.Errorf("embedded data URI exceeds cap")
	}
	raw, err := url.PathUnescape(uri.payload)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxEmbeddedDataBytes {
		return nil, fmt.Errorf("embedded data URI exceeds cap")
	}
	return []byte(raw), nil
}

func embeddedDataFormat(raw []byte, mime string) string {
	if format := detectImageFormat(raw); format != "unknown" {
		return format
	}
	if strings.Contains(strings.ToLower(mime), "svg") || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<svg")) {
		return "svg"
	}
	return "unknown"
}

func escapeDataURIBytes(data []byte) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, value := range data {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' || value == '-' || value == '.' || value == '_' || value == '~' || value == '/' {
			out.WriteByte(value)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[value>>4])
		out.WriteByte(hex[value&0x0f])
	}
	return out.String()
}

func inspectEmbeddedDataURIs(text string) (bool, bool, []string) {
	var c2pa, ai bool
	findings := []string{}
	for _, uri := range iterDataURIs(text) {
		raw, err := decodeDataURI(uri)
		if err != nil || len(raw) == 0 {
			continue
		}
		fmtName := embeddedDataFormat(raw, uri.mime)
		if fmtName == "unknown" {
			continue
		}
		var report FileReport
		if fmtName == "svg" {
			c2, a, found, details := inspectSVG(raw)
			report = FileReport{Format: fmtName, HasC2PA: c2, HasAIMetadata: a, Findings: found, Details: details}
		} else {
			report = inspectImageWithOptions(raw, "embedded", DefaultOptions(), false)
		}
		if report.HasAIMetadata || report.HasC2PA {
			ai = true
			c2pa = c2pa || report.HasC2PA
			findings = append(findings, "embedded data:image/"+fmtName+": "+strings.Join(report.Findings, ", "))
		}
	}
	return c2pa, ai, findings
}

func cleanEmbeddedDataURIs(text string, opts Options) (string, []string) {
	actions := []string{}
	var out strings.Builder
	last := 0
	for _, uri := range iterDataURIs(text) {
		raw, err := decodeDataURI(uri)
		if err != nil || len(raw) == 0 {
			continue
		}
		fmtName := embeddedDataFormat(raw, uri.mime)
		if fmtName == "unknown" {
			continue
		}
		var cleaned []byte
		if fmtName == "svg" {
			cleaned, _, err = cleanSVG(raw, opts)
		} else {
			cleaned, _, err = cleanImage(raw, fmtName, opts)
		}
		if err != nil || sameBytes(raw, cleaned) {
			continue
		}
		out.WriteString(text[last:uri.start])
		out.WriteString("data:image/")
		out.WriteString(uri.mime)
		out.WriteString(uri.params)
		out.WriteByte(',')
		if uri.base64 {
			out.WriteString(base64.StdEncoding.EncodeToString(cleaned))
		} else {
			out.WriteString(escapeDataURIBytes(cleaned))
		}
		last = uri.end
		actions = append(actions, "clean embedded data:image/"+fmtName)
	}
	if last == 0 {
		return text, actions
	}
	out.WriteString(text[last:])
	return out.String(), actions
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
func commandAvailable(name string) bool { _, err := exec.LookPath(name); return err == nil }
