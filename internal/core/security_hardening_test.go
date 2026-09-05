package core

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"
)

func securityPNGChunk(ctype, payload []byte) []byte {
	return pngChunk(ctype, payload)
}

func securityPNGWithChunks(chunks ...[]byte) []byte {
	data := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 1)
	binary.BigEndian.PutUint32(ihdr[4:8], 1)
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 2, 0, 0, 0
	data = append(data, pngChunk([]byte("IHDR"), ihdr)...)
	for _, chunk := range chunks {
		data = append(data, chunk...)
	}
	return append(data, pngChunk([]byte("IEND"), nil)...)
}

func TestSecurityHardeningXMLAndHTMLScansRemainLinear(t *testing.T) {
	flood := strings.Repeat("<metadata>", 256*1024/len("<metadata>"))
	start := time.Now()
	cleaned, actions, err := cleanSVG([]byte(flood), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("SVG metadata scan took %s", elapsed)
	}
	if string(cleaned) != flood || containsString(actions, "drop <metadata> x1") {
		t.Fatalf("unclosed SVG metadata changed: actions=%v", actions)
	}

	htmlFlood := strings.Repeat(`<script type="application/ld+json">`, 128*1024/len(`<script type="application/ld+json">`))
	start = time.Now()
	cleaned, _, err = cleanHTML([]byte(htmlFlood), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("HTML JSON-LD scan took %s", elapsed)
	}
	if string(cleaned) != htmlFlood {
		t.Fatal("unclosed HTML JSON-LD tags changed")
	}
}

func TestSecurityHardeningSVGDeclarationContext(t *testing.T) {
	input := []byte(`<?xml version="1.0"?>
<!DOCTYPE svg [ <!-- ]> --> <!ENTITY x "y"> ]>
<svg xmlns="http://www.w3.org/2000/svg"><![CDATA[<!DOCTYPE keep>]]><!-- <!ENTITY keep> --></svg>`)
	cleaned, actions, err := cleanSVG(input, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	got := string(cleaned)
	if strings.Contains(got, "<!DOCTYPE svg") || strings.Contains(got, "<!ENTITY x") {
		t.Fatalf("declaration survived: %s", got)
	}
	if !strings.Contains(got, "<!DOCTYPE keep>") || !strings.Contains(got, "<!ENTITY keep>") {
		t.Fatalf("declaration-like content was not preserved: %s", got)
	}
	if !containsString(actions, "drop DOCTYPE/entity declarations x1") {
		t.Fatalf("declaration action missing: %v", actions)
	}
}

func TestSecurityHardeningPNGTextCapStillScansLaterChunks(t *testing.T) {
	original := maxPNGTextDecompressedBytes
	maxPNGTextDecompressedBytes = 64
	t.Cleanup(func() { maxPNGTextDecompressedBytes = original })
	bombText := bytes.Repeat([]byte("x"), 1000)
	var compressed bytes.Buffer
	zw, err := zlib.NewWriterLevel(&compressed, zlib.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = zw.Write(bombText)
	_ = zw.Close()
	bomb := append([]byte("Comment\x00\x00"), compressed.Bytes()...)
	software := []byte("Software\x00ChatGPT")
	data := securityPNGWithChunks(
		securityPNGChunk([]byte("zTXt"), bomb),
		securityPNGChunk([]byte("tEXt"), software),
	)
	_, ai, findings := inspectPNG(data)
	if !ai || !containsStringWith(findings, "exceeds cap") || !containsStringWith(findings, "ChatGPT") {
		t.Fatalf("PNG cap/later chunk handling failed: ai=%v findings=%v", ai, findings)
	}
	cleaned, actions := stripPNG(data, false)
	if bytes.Contains(cleaned, []byte("zTXt")) || !containsStringWith(actions, "exceeds cap") {
		t.Fatalf("over-budget PNG chunk survived: actions=%v", actions)
	}
}

func TestSecurityHardeningZipChargesRealBytes(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 64)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("part.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(w, bytes.NewReader(payload))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(0)
	got, err := readZipMemberBounded(zr.File[0], &budget)
	if err != nil || !bytes.Equal(got, payload) || budget != int64(len(payload)) {
		t.Fatalf("zip budget charged incorrectly: len=%d budget=%d err=%v", len(got), budget, err)
	}
}

func containsStringWith(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
