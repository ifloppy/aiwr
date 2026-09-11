package core

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".avif": true,
	".heic": true, ".heif": true, ".bmp": true, ".gif": true, ".tiff": true, ".tif": true,
}

var containerExts = map[string]bool{
	".svg": true, ".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
	".odt": true, ".epub": true, ".html": true, ".htm": true,
	".md": true, ".markdown": true, ".mdx": true,
	".tex": true, ".ltx": true,
}

var textExts = map[string]bool{}

var avExts = map[string]bool{
	".mp4": true, ".mov": true, ".m4a": true, ".m4v": true,
	".wav": true, ".mp3": true, ".flac": true,
}

func init() {
	for _, ext := range strings.Fields(`.txt .text .css .js .jsx .mjs .cjs .ts .tsx .gd .gdshader .py .rs .go .json .yaml .yml .toml .csv .c .h .cpp .cc .hpp .cs .java .kt .kts .swift .scala .dart .rb .php .lua .pl .r .sh .bash .zsh .ps1 .sql .vue .svelte .astro .rst .adoc .asciidoc .org .po .pot .strings .arb .resx .properties .ini .cfg .conf .tsv`) {
		textExts[ext] = true
	}
}

func classifyByExtension(path string) (Kind, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case imageExts[ext]:
		return KindImage, true
	case containerExts[ext]:
		return KindContainer, true
	case textExts[ext]:
		return KindText, true
	case avExts[ext]:
		return KindAV, true
	default:
		return KindUnknown, false
	}
}

func classifyBytes(data []byte, suffix string) Kind {
	if kind, ok := classifyByExtension("input" + suffix); ok {
		return kind
	}
	if detectImageFormat(data) != "unknown" {
		return KindImage
	}
	if detectAVFormat(data) != "unknown" {
		return KindAV
	}
	if detectContainerFormat("input"+suffix, data) != "unknown" {
		return KindContainer
	}
	return KindUnknown
}

func Classify(path string) (Kind, error) {
	if kind, ok := classifyByExtension(path); ok {
		return kind, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return KindUnknown, err
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return KindUnknown, err
	}
	head = head[:n]
	if detectImageFormat(head) != "unknown" || detectAVFormat(head) != "unknown" {
		return classifyBytes(head, filepath.Ext(path)), nil
	}
	// ZIP-based formats identify themselves in the central directory. Avoid a
	// second full read for all other files; PDF/SVG and similar text containers
	// expose their signature in the header.
	data := head
	if bytes.HasPrefix(head, []byte("PK\x03\x04")) {
		var readErr error
		data, readErr = os.ReadFile(path)
		if readErr != nil {
			return KindUnknown, readErr
		}
	}
	if detectContainerFormat("input"+filepath.Ext(path), data) != "unknown" {
		return KindContainer, nil
	}
	return KindUnknown, nil
}

func detectImageFormat(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "png"
	case len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8:
		return "jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif"
	case bytes.HasPrefix(data, []byte("BM")):
		return "bmp"
	case bytes.HasPrefix(data, []byte("II*\x00")), bytes.HasPrefix(data, []byte("MM\x00*")),
		bytes.HasPrefix(data, []byte("II+\x00")), bytes.HasPrefix(data, []byte("MM\x00+")):
		return "tiff"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "webp"
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		boxEnd := minInt(len(data), 64)
		boxSize := readUint32BE(data[:4])
		if boxSize >= 8 && uint64(boxSize) < uint64(boxEnd) {
			boxEnd = int(boxSize)
		}
		headerChunk := data[8:boxEnd]
		for _, brand := range [][]byte{[]byte("avif"), []byte("avis"), []byte("avio")} {
			if bytes.Contains(headerChunk, brand) {
				return "avif"
			}
		}
		for _, brand := range [][]byte{[]byte("heic"), []byte("heix"), []byte("hevc"), []byte("heim"), []byte("heis"), []byte("mif1"), []byte("msf1"), []byte("heif")} {
			if bytes.Contains(headerChunk, brand) {
				return "heic"
			}
		}
	}
	return "unknown"
}

func detectContainerFormat(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".svg":
		return "svg"
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	case ".xlsx":
		return "xlsx"
	case ".pptx":
		return "pptx"
	case ".odt":
		return "odt"
	case ".epub":
		return "epub"
	case ".html", ".htm":
		return "html"
	case ".md", ".markdown", ".mdx":
		return "markdown"
	case ".tex", ".ltx":
		return "latex"
	}
	if bytes.HasPrefix(data, []byte("%PDF")) {
		return "pdf"
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<")) && bytes.Contains(bytes.ToLower(trimmed[:minInt(len(trimmed), 500)]), []byte("svg")) {
		return "svg"
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "unknown"
		}
		names := make(map[string]bool, len(zr.File))
		for _, f := range zr.File {
			names[f.Name] = true
		}
		if names["mimetype"] {
			for _, f := range zr.File {
				if f.Name != "mimetype" {
					continue
				}
				r, openErr := f.Open()
				if openErr != nil {
					break
				}
				b, _ := io.ReadAll(io.LimitReader(r, 256))
				r.Close()
				mt := strings.ToLower(string(bytes.TrimSpace(b)))
				if strings.Contains(mt, "epub") {
					return "epub"
				}
				if strings.Contains(mt, "opendocument") || strings.Contains(mt, "oasis") {
					return "odt"
				}
			}
		}
		for name := range names {
			if name == "word/document.xml" || strings.HasPrefix(name, "word/") {
				return "docx"
			}
			if name == "xl/workbook.xml" || strings.HasPrefix(name, "xl/") {
				return "xlsx"
			}
			if name == "ppt/presentation.xml" || strings.HasPrefix(name, "ppt/") {
				return "pptx"
			}
		}
		if names["content.xml"] && (names["meta.xml"] || names["META-INF/manifest.xml"]) {
			return "odt"
		}
		if names["META-INF/container.xml"] {
			for name := range names {
				if strings.HasSuffix(name, ".opf") {
					return "epub"
				}
			}
		}
	}
	return "unknown"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isBinary(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	magic := []struct {
		prefix []byte
		label  string
	}{
		{[]byte("PK\x03\x04"), "a ZIP container (DOCX, ODT, XLSX, PPTX, EPUB, JAR)"},
		{[]byte("PK\x05\x06"), "an empty ZIP container"}, {[]byte("PK\x07\x08"), "a spanned ZIP container"},
		{[]byte("%PDF-"), "a PDF"}, {[]byte{0x89, 'P', 'N', 'G'}, "a PNG image"},
		{[]byte{0xff, 0xd8, 0xff}, "a JPEG image"}, {[]byte("GIF87a"), "a GIF image"},
		{[]byte("GIF89a"), "a GIF image"}, {[]byte("BM"), "a BMP image"},
		{[]byte("II*\x00"), "a TIFF image"}, {[]byte("MM\x00*"), "a TIFF image"},
		{[]byte("RIFF"), "a RIFF container (WEBP, WAV, AVI)"}, {[]byte("OggS"), "an Ogg media file"},
		{[]byte{0x1f, 0x8b}, "a gzip archive"}, {[]byte("BZh"), "a bzip2 archive"},
		{[]byte{0xfd, '7', 'z', 'X', 'Z', 0x00}, "an xz archive"},
		{[]byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, "a 7-Zip archive"},
		{[]byte("Rar!\x1a\x07"), "a RAR archive"}, {[]byte{0x7f, 'E', 'L', 'F'}, "an ELF binary"},
		{[]byte{0xca, 0xfe, 0xba, 0xbe}, "a Java class or Mach-O fat binary"},
		{[]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, "a legacy Office document (.doc, .xls, .ppt)"},
		{[]byte("SQLite format 3\x00"), "a SQLite database"}, {[]byte("8BPS"), "a Photoshop document"},
		{[]byte("wOFF"), "a WOFF font"}, {[]byte("wOF2"), "a WOFF2 font"},
		{[]byte{0x00, 0x01, 0x00, 0x00, 0x00}, "a TrueType font"}, {[]byte("OTTO"), "an OpenType font"},
		{[]byte("fLaC"), "a FLAC audio file"}, {[]byte("ID3"), "an ID3 media file"},
	}
	for _, m := range magic {
		if bytes.HasPrefix(data, m.prefix) {
			return m.label
		}
	}
	head := data[:minInt(len(data), 8192)]
	for _, b := range head {
		if b == 0 {
			return "binary data (contains NUL bytes)"
		}
	}
	allowed := map[byte]bool{9: true, 10: true, 11: true, 12: true, 13: true, 27: true}
	controls := 0
	for _, b := range head {
		if b < 0x20 && !allowed[b] {
			controls++
		}
	}
	if float64(controls)/float64(len(head)) > 0.05 {
		return "binary data (dense in control bytes)"
	}
	return ""
}

func readUint32BE(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
