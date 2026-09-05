package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/iruanp/aiwr/internal/core"
)

const (
	websiteDefaultMaxBytes       = 4 << 20
	websiteDefaultTimeoutSeconds = 15
	websiteDefaultMaxPages       = 200
	websiteSitemapMaxBytes       = 64 << 20
	websiteMaxRedirects          = 5
)

type websiteOrigin struct {
	scheme string
	host   string
	port   int
}

func runAuditWebsite(args []string) int {
	sitemapURL, baseURL := "", ""
	maxPages, timeoutSeconds, maxBytes := websiteDefaultMaxPages, websiteDefaultTimeoutSeconds, websiteDefaultMaxBytes
	format := "human"
	jsonOutput, sarifOutput, checkStylometry := false, false, false
	fs := newFlagSet("audit-website")
	fs.StringVar(&sitemapURL, "sitemap", sitemapURL, "sitemap URL to audit")
	fs.StringVar(&baseURL, "base", baseURL, "base URL; discover sitemap automatically")
	fs.IntVar(&maxPages, "max-pages", maxPages, "maximum URLs to scan")
	fs.IntVar(&timeoutSeconds, "timeout", timeoutSeconds, "per-request timeout in seconds")
	fs.IntVar(&maxBytes, "max-bytes", maxBytes, "maximum bytes per downloaded asset")
	fs.StringVar(&format, "format", format, "output format: human, json, or sarif")
	fs.BoolVar(&jsonOutput, "json", false, "emit JSON")
	fs.BoolVar(&sarifOutput, "sarif", false, "emit SARIF 2.1.0")
	fs.BoolVar(&checkStylometry, "check-stylometry", false, "include heuristic text stylometry")
	if err := fs.Parse(args); err != nil {
		return cliError(err)
	}
	if fs.NArg() != 0 {
		return cliError(errors.New("audit-website does not accept positional arguments"))
	}
	if jsonOutput || sarifOutput {
		if format != "human" || jsonOutput && sarifOutput {
			return cliError(errors.New("--format, --json, and --sarif are mutually exclusive"))
		}
		if jsonOutput {
			format = "json"
		} else {
			format = "sarif"
		}
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "human" && format != "json" && format != "sarif" {
		return cliError(fmt.Errorf("invalid --format %q; choose human, json, or sarif", format))
	}
	if strings.TrimSpace(sitemapURL) == "" && strings.TrimSpace(baseURL) == "" {
		return cliError(errors.New("provide --sitemap URL or --base URL"))
	}
	if maxPages < 1 || timeoutSeconds < 1 || maxBytes < 1 {
		return cliError(errors.New("--max-pages, --timeout, and --max-bytes must be positive"))
	}

	if sitemapURL == "" {
		var err error
		sitemapURL, err = discoverWebsiteSitemap(baseURL, time.Duration(timeoutSeconds)*time.Second)
		if err != nil {
			return cliError(fmt.Errorf("invalid base URL: %w", err))
		}
		if sitemapURL == "" {
			return cliError(fmt.Errorf("no sitemap found for %s", baseURL))
		}
	}
	urls, err := collectWebsiteURLs(sitemapURL, time.Duration(timeoutSeconds)*time.Second, maxPages)
	if err != nil {
		return cliError(fmt.Errorf("could not collect URLs from %s: %w", sitemapURL, err))
	}
	if len(urls) == 0 {
		return cliError(errors.New("no URLs collected from sitemap"))
	}

	opts := core.DefaultOptions()
	opts.Stylometry = checkStylometry
	files := make([]map[string]any, 0, len(urls))
	failures := make([]map[string]any, 0)
	for _, itemURL := range urls[:minWebsiteInt(len(urls), maxPages)] {
		data, contentType, fetchErr := fetchWebsite(itemURL, time.Duration(timeoutSeconds)*time.Second, maxBytes, nil)
		if fetchErr != nil {
			failures = append(failures, map[string]any{"url": itemURL, "error": fetchErr.Error()})
			continue
		}
		report, inspectErr := inspectWebsiteRemote(itemURL, data, contentType, opts)
		if inspectErr != nil {
			failures = append(failures, map[string]any{"url": itemURL, "error": "inspect failed: " + inspectErr.Error()})
			continue
		}
		files = append(files, auditItem(report, opts))
	}

	summary := aggregateAudit(files)
	report := map[string]any{
		"sitemap":        sitemapURL,
		"base":           baseURL,
		"urls_collected": len(urls),
		"urls_scanned":   len(files),
		"urls_failed":    failures,
		"summary":        summary,
		"files":          files,
	}
	switch format {
	case "json":
		writeJSON(report)
	case "sarif":
		writeJSON(makeAuditSARIF(report))
	default:
		fmt.Printf("Sitemap: %s\nURLs collected: %d\nURLs scanned: %d\nURLs failed: %d\n", sitemapURL, len(urls), len(files), len(failures))
		fmt.Printf("By kind: %v\nWith C2PA: %v\nWith AI metadata: %v\nWith suspicious text: %v\nActionable files: %v\nFindings by confidence: %v\n", summary["by_kind"], summary["with_c2pa"], summary["with_ai_metadata"], summary["with_suspicious_text"], summary["actionable_files"], summary["findings_by_confidence"])
		for _, item := range files {
			findings, _ := item["findings"].([]string)
			confidence, _ := item["confidence"].([]string)
			for i, finding := range findings {
				level := ""
				if i < len(confidence) {
					level = confidence[i]
				}
				fmt.Printf("  [%s] %v: %s\n", level, item["path"], finding)
			}
		}
		for _, failure := range failures {
			fmt.Printf("  [error] %v: %v\n", failure["url"], failure["error"])
		}
	}
	if len(failures) > 0 {
		return 3
	}
	if value, _ := summary["actionable_files"].(int); value > 0 {
		return 1
	}
	return 0
}

func websiteOriginFromURL(raw string) (websiteOrigin, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return websiteOrigin{}, err
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return websiteOrigin{}, fmt.Errorf("unsupported URL scheme: %s", parsed.Scheme)
	}
	if parsed.User != nil {
		return websiteOrigin{}, errors.New("credentials in URLs are not allowed")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return websiteOrigin{}, errors.New("URL has no hostname")
	}
	port := 80
	if scheme == "https" {
		port = 443
	}
	if rawPort := parsed.Port(); rawPort != "" {
		value, convErr := strconv.Atoi(rawPort)
		if convErr != nil || value < 1 || value > 65535 {
			return websiteOrigin{}, fmt.Errorf("invalid URL port: %s", rawPort)
		}
		port = value
	}
	return websiteOrigin{scheme: scheme, host: host, port: port}, nil
}

func websiteOriginAllowed(candidate, expected websiteOrigin) bool {
	if candidate == expected {
		return true
	}
	return expected.scheme == "http" && expected.port == 80 && candidate.scheme == "https" && candidate.port == 443 && candidate.host == expected.host
}

func websiteAddressPublic(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	if address.Is4() {
		v4 := address.As4()
		// CGNAT and documentation/reserved IPv4 ranges are not public targets.
		if v4[0] == 100 && v4[1]&0xc0 == 64 || v4[0] == 192 && v4[1] == 0 && v4[2] == 0 || v4[0] == 192 && v4[1] == 0 && v4[2] == 2 || v4[0] == 198 && v4[1] == 18 || v4[0] == 198 && v4[1] == 19 || v4[0] == 198 && v4[1] == 51 && v4[2] == 100 || v4[0] == 203 && v4[1] == 0 && v4[2] == 113 || v4[0] >= 240 {
			return false
		}
	}
	if address.Is6() {
		if netip.MustParsePrefix("2001:db8::/32").Contains(address) || netip.MustParsePrefix("2001:10::/28").Contains(address) {
			return false
		}
	}
	return true
}

func resolveWebsiteAddresses(origin websiteOrigin) ([]string, error) {
	var ips []net.IP
	if ip := net.ParseIP(origin.host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(origin.host)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve hostname %s: %w", origin.host, err)
		}
		ips = resolved
	}
	addresses := make([]string, 0, len(ips))
	seen := map[string]bool{}
	for _, ip := range ips {
		address, parseErr := netip.ParseAddr(strings.SplitN(ip.String(), "%", 2)[0])
		if parseErr != nil || !websiteAddressPublic(address) {
			return nil, fmt.Errorf("refusing non-public address for %s: %s", origin.host, ip)
		}
		canonical := address.Unmap().String()
		if !seen[canonical] {
			seen[canonical] = true
			addresses = append(addresses, canonical)
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("hostname resolved to no public IP addresses: %s", origin.host)
	}
	return addresses, nil
}

func websitePinnedClient(origin websiteOrigin, addresses []string, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}
	serverName := origin.host
	if net.ParseIP(serverName) != nil {
		serverName = ""
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName},
		ResponseHeaderTimeout: timeout,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, address := range addresses {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address, strconv.Itoa(origin.port)))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = errors.New("no validated address available")
			}
			return nil, lastErr
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func readWebsiteCapped(reader io.Reader, maxBytes int, label string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	return data, nil
}

func fetchWebsite(raw string, timeout time.Duration, maxBytes int, allowedOrigin *websiteOrigin) ([]byte, string, error) {
	current := raw
	for redirects := 0; redirects <= websiteMaxRedirects; redirects++ {
		origin, err := websiteOriginFromURL(current)
		if err != nil {
			return nil, "", err
		}
		if allowedOrigin != nil && !websiteOriginAllowed(origin, *allowedOrigin) {
			return nil, "", fmt.Errorf("cross-origin URL is not allowed: %s://%s:%d", origin.scheme, origin.host, origin.port)
		}
		addresses, err := resolveWebsiteAddresses(origin)
		if err != nil {
			return nil, "", err
		}
		client := websitePinnedClient(origin, addresses, timeout)
		request, err := http.NewRequest(http.MethodGet, current, nil)
		if err != nil {
			client.Transport.(*http.Transport).CloseIdleConnections()
			return nil, "", err
		}
		request.Header.Set("User-Agent", "aiwr/audit-website")
		request.Header.Set("Connection", "close")
		response, err := client.Do(request)
		if err != nil {
			client.Transport.(*http.Transport).CloseIdleConnections()
			return nil, "", err
		}
		if response.StatusCode >= 300 && response.StatusCode <= 399 {
			location := response.Header.Get("Location")
			if location != "" {
				_ = response.Body.Close()
				client.Transport.(*http.Transport).CloseIdleConnections()
				next, parseErr := url.Parse(location)
				if parseErr != nil {
					return nil, "", parseErr
				}
				base, _ := url.Parse(current)
				current = base.ResolveReference(next).String()
				continue
			}
		}
		contentType := response.Header.Get("Content-Type")
		data, readErr := readWebsiteCapped(response.Body, maxBytes, "response")
		_ = response.Body.Close()
		client.Transport.(*http.Transport).CloseIdleConnections()
		if readErr != nil {
			return nil, "", readErr
		}
		return data, contentType, nil
	}
	return nil, "", fmt.Errorf("too many redirects (>%d)", websiteMaxRedirects)
}

func parseWebsiteSitemap(data []byte) (string, []string, error) {
	if len(data) > websiteSitemapMaxBytes {
		return "", nil, fmt.Errorf("sitemap size exceeds %d bytes", websiteSitemapMaxBytes)
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", nil, err
		}
		data, err = readWebsiteCapped(reader, websiteSitemapMaxBytes, "sitemap decompressed size")
		_ = reader.Close()
		if err != nil {
			return "", nil, err
		}
	} else if len(data) > websiteSitemapMaxBytes {
		return "", nil, fmt.Errorf("sitemap size exceeds %d bytes", websiteSitemapMaxBytes)
	}
	upper := bytes.ToUpper(data)
	if bytes.Contains(upper, []byte("<!DOCTYPE")) || bytes.Contains(upper, []byte("<!ENTITY")) {
		return "", nil, errors.New("sitemap declares a DTD / entities; refusing to parse")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root := ""
	urls := []string{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if root == "" {
				root = value.Name.Local
			}
			if value.Name.Local == "loc" {
				var location string
				if err := decoder.DecodeElement(&location, &value); err != nil {
					return "", nil, err
				}
				if location = strings.TrimSpace(location); location != "" {
					urls = append(urls, location)
				}
			}
		}
	}
	if root == "" {
		return "", nil, errors.New("sitemap has no root element")
	}
	return root, urls, nil
}

func discoverWebsiteSitemap(baseURL string, timeout time.Duration) (string, error) {
	origin, err := websiteOriginFromURL(baseURL)
	if err != nil {
		return "", err
	}
	if _, err := resolveWebsiteAddresses(origin); err != nil {
		return "", err
	}
	base := strings.TrimRight(baseURL, "/")
	for _, candidate := range []string{base + "/sitemap.xml", base + "/sitemap_index.xml"} {
		data, _, fetchErr := fetchWebsite(candidate, timeout, websiteDefaultMaxBytes, &origin)
		if fetchErr != nil {
			continue
		}
		if _, _, parseErr := parseWebsiteSitemap(data); parseErr == nil {
			return candidate, nil
		}
	}
	data, _, fetchErr := fetchWebsite(base+"/robots.txt", timeout, 1<<20, &origin)
	if fetchErr != nil {
		return "", nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "sitemap:") {
			continue
		}
		candidate := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		candidateOrigin, originErr := websiteOriginFromURL(candidate)
		if originErr != nil {
			return "", originErr
		}
		if !websiteOriginAllowed(candidateOrigin, origin) {
			return "", fmt.Errorf("cross-origin sitemap is not allowed: %s", candidate)
		}
		return candidate, nil
	}
	return "", nil
}

func collectWebsiteURLs(sitemapURL string, timeout time.Duration, maxPages int) ([]string, error) {
	origin, err := websiteOriginFromURL(sitemapURL)
	if err != nil {
		return nil, err
	}
	if _, err := resolveWebsiteAddresses(origin); err != nil {
		return nil, err
	}
	urls := []string{}
	seen := map[string]bool{}
	var visit func(string, int) error
	visit = func(current string, depth int) error {
		if len(urls) >= maxPages || depth > 3 {
			return nil
		}
		data, _, fetchErr := fetchWebsite(current, timeout, websiteDefaultMaxBytes, &origin)
		if fetchErr != nil {
			return fetchErr
		}
		kind, locations, parseErr := parseWebsiteSitemap(data)
		if parseErr != nil {
			return parseErr
		}
		for _, location := range locations {
			candidateOrigin, originErr := websiteOriginFromURL(location)
			if originErr != nil {
				return originErr
			}
			if !websiteOriginAllowed(candidateOrigin, origin) {
				return fmt.Errorf("cross-origin sitemap URL is not allowed: %s", location)
			}
			if seen[location] {
				continue
			}
			seen[location] = true
			if strings.EqualFold(kind, "sitemapindex") {
				if err := visit(location, depth+1); err != nil {
					return err
				}
				if len(urls) >= maxPages {
					break
				}
				continue
			}
			urls = append(urls, location)
			if len(urls) >= maxPages {
				break
			}
		}
		return nil
	}
	if err := visit(sitemapURL, 0); err != nil {
		return nil, err
	}
	return urls, nil
}

func websiteGuessKind(rawURL string, data []byte, contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if strings.Contains(contentType, "html") {
		return "html"
	}
	contentTypes := map[string]string{
		"image/png": "png", "image/jpeg": "jpeg", "image/svg+xml": "svg", "application/pdf": "pdf",
		"image/webp": "webp", "image/avif": "avif", "image/heic": "heic", "image/heif": "heic",
		"image/gif": "gif", "image/bmp": "bmp", "image/tiff": "tiff", "application/epub+zip": "epub",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
		"application/vnd.oasis.opendocument.text":                                   "odt", "text/markdown": "markdown", "text/plain": "text",
		"video/mp4": "mp4", "video/quicktime": "mp4", "audio/x-m4a": "mp4", "audio/mp4": "mp4",
		"audio/wav": "wav", "audio/x-wav": "wav", "audio/mpeg": "mp3", "audio/mp3": "mp3",
	}
	if kind, ok := contentTypes[contentType]; ok {
		return kind
	}
	pathName := ""
	if parsed, err := url.Parse(rawURL); err == nil {
		pathName = strings.ToLower(parsed.Path)
	}
	for _, item := range []struct {
		ext  string
		kind string
	}{
		{".png", "png"}, {".jpg", "jpeg"}, {".jpeg", "jpeg"}, {".webp", "webp"}, {".avif", "avif"},
		{".heic", "heic"}, {".heif", "heic"}, {".gif", "gif"}, {".bmp", "bmp"}, {".tiff", "tiff"}, {".tif", "tiff"},
		{".svg", "svg"}, {".pdf", "pdf"}, {".docx", "docx"}, {".xlsx", "xlsx"}, {".pptx", "pptx"}, {".odt", "odt"},
		{".epub", "epub"}, {".mp4", "mp4"}, {".mov", "mp4"}, {".m4a", "mp4"}, {".wav", "wav"}, {".mp3", "mp3"},
		{".html", "html"}, {".htm", "html"}, {".md", "markdown"}, {".markdown", "markdown"}, {".txt", "text"},
	} {
		if strings.HasSuffix(pathName, item.ext) {
			return item.kind
		}
	}
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return "png"
	}
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8 {
		return "jpeg"
	}
	if bytes.HasPrefix(data, []byte("%PDF")) {
		return "pdf"
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "webp"
	}
	if bytes.HasPrefix(data, []byte("GIF8")) {
		return "gif"
	}
	if bytes.HasPrefix(data, []byte("BM")) {
		return "bmp"
	}
	if bytes.HasPrefix(data, []byte("II*\x00")) || bytes.HasPrefix(data, []byte("MM\x00*")) {
		return "tiff"
	}
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		switch brand {
		case "avif", "avis":
			return "avif"
		case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
			return "heic"
		default:
			return "mp4"
		}
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		if kind := websiteZipKind(data); kind != "" {
			return kind
		}
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '<' && bytes.Contains(bytes.ToLower(trimmed[:minWebsiteInt(len(trimmed), 500)]), []byte("svg")) {
		return "svg"
	}
	if bytes.Contains(bytes.ToLower(data[:minWebsiteInt(len(data), 2000)]), []byte("<html")) || len(trimmed) > 0 && trimmed[0] == '<' {
		return "html"
	}
	return "text"
}

func websiteZipKind(data []byte) string {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
		if file.Name == "mimetype" {
			opened, openErr := file.Open()
			if openErr == nil {
				mime, readErr := readWebsiteCapped(opened, 256, "mimetype")
				_ = opened.Close()
				if readErr == nil {
					value := strings.ToLower(strings.TrimSpace(string(mime)))
					if strings.Contains(value, "epub") {
						return "epub"
					}
					if strings.Contains(value, "opendocument") || strings.Contains(value, "oasis") {
						return "odt"
					}
				}
			}
		}
	}
	if names["word/document.xml"] || websiteHasPrefix(names, "word/") {
		return "docx"
	}
	if names["xl/workbook.xml"] || websiteHasPrefix(names, "xl/") {
		return "xlsx"
	}
	if names["ppt/presentation.xml"] || websiteHasPrefix(names, "ppt/") {
		return "pptx"
	}
	if names["content.xml"] && (names["meta.xml"] || names["META-INF/manifest.xml"]) {
		return "odt"
	}
	if names["META-INF/container.xml"] && websiteHasSuffix(names, ".opf") {
		return "epub"
	}
	return ""
}

func websiteHasPrefix(values map[string]bool, prefix string) bool {
	for value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func websiteHasSuffix(values map[string]bool, suffix string) bool {
	for value := range values {
		if strings.HasSuffix(strings.ToLower(value), suffix) {
			return true
		}
	}
	return false
}

func inspectWebsiteRemote(rawURL string, data []byte, contentType string, opts core.Options) (core.FileReport, error) {
	kind := websiteGuessKind(rawURL, data, contentType)
	extensions := map[string]string{
		"webp": ".webp", "avif": ".avif", "heic": ".heic", "gif": ".gif", "bmp": ".bmp", "tiff": ".tiff",
		"xlsx": ".xlsx", "pptx": ".pptx", "epub": ".epub", "mp4": ".mp4", "wav": ".wav", "mp3": ".mp3",
		"png": ".png", "jpeg": ".jpg", "svg": ".svg", "pdf": ".pdf", "docx": ".docx", "odt": ".odt",
		"html": ".html", "markdown": ".md", "text": ".txt",
	}
	ext := extensions[kind]
	if ext == "" {
		ext = ".txt"
	}
	if kind == "text" {
		opts.ForceType, opts.ForceText = "text", true
	} else if kind == "image" || kind == "png" || kind == "jpeg" || kind == "webp" || kind == "avif" || kind == "heic" || kind == "gif" || kind == "bmp" || kind == "tiff" {
		opts.ForceType = "image"
	} else if kind == "mp4" || kind == "wav" || kind == "mp3" {
		opts.ForceType = "av"
	} else {
		opts.ForceType = "container"
	}
	opts.DisableExternalTools = true
	report, err := core.InspectBytesWithExternal(data, "remote"+ext, opts, false)
	if err != nil {
		return core.FileReport{}, err
	}
	report.Path = rawURL
	if report.Format == "" || report.Format == "unknown" {
		report.Format = kind
	}
	return report, nil
}

func minWebsiteInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
