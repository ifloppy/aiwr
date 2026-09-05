package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const pdfToolTimeout = 5 * time.Minute
const pdfExiftoolTimeout = 60 * time.Second
const pdfQPDFTimeout = 2 * time.Minute

const defaultPDFCleanBudget = 420 * time.Second

var pdfJPEGMetadataMarkers = map[byte]bool{0xe1: true, 0xeb: true, 0xed: true}
var pdfJPEGProvenanceMarkers = map[byte]bool{0xe1: true, 0xeb: true}
var pdfJPEGProvenanceSignatures = [][]byte{
	[]byte("jumbf"), []byte("c2pa"), []byte("contentauth"), []byte("dcterms:provenance"),
}

type pdfDeadline struct {
	end time.Time
}

func newPDFDeadline() *pdfDeadline {
	budget := defaultPDFCleanBudget
	if raw := strings.TrimSpace(os.Getenv("WATERMARKS_PDF_CLEAN_BUDGET")); raw != "" {
		if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds > 0 {
			budget = time.Duration(seconds * float64(time.Second))
		}
	}
	return &pdfDeadline{end: time.Now().Add(budget)}
}

func (d *pdfDeadline) spent() bool {
	return d != nil && time.Until(d.end) <= 0
}

func (d *pdfDeadline) timeout(cap time.Duration) time.Duration {
	if d == nil {
		return cap
	}
	remaining := time.Until(d.end)
	if remaining <= 0 {
		return time.Second
	}
	if remaining < cap {
		return remaining
	}
	return cap
}

// cleanPDF performs a byte-preserving fallback when optional PDF tools are
// unavailable, then uses qpdf/ExifTool/Ghostscript when present. All tool
// paths and temporary files are generated internally; user-controlled
// filenames never become shell input.
func cleanPDF(data []byte, opts Options) ([]byte, []string, error) {
	switch opts.DeepImages {
	case DeepAuto, DeepAlways, DeepLossless, DeepNever:
	default:
		return nil, nil, fmt.Errorf("invalid deep image mode %q", opts.DeepImages)
	}
	actions := []string{}
	tempDir, err := os.MkdirTemp("", "aiwr-pdf-")
	if err != nil {
		return nil, actions, err
	}
	defer os.RemoveAll(tempDir)
	source := filepath.Join(tempDir, "source.pdf")

	deadline := newPDFDeadline()
	var exiftool string
	if !opts.DisableExternalTools && stripAllMetadata(opts) {
		exiftool, _ = exec.LookPath("exiftool")
	}

	// ExifTool edits PDFs incrementally. Run qpdf immediately afterward so the
	// original metadata objects do not remain recoverable in the output.
	if exiftool != "" {
		if err := os.WriteFile(source, data, 0o600); err != nil {
			return nil, actions, err
		}
		if ok, detail := runPDFExiftool(exiftool, source, deadline); ok {
			actions = append(actions, "exiftool -all= document metadata strip")
			pdfStructuralRewrite(source, &actions, deadline)
		} else {
			actions = append(actions, "warning: exiftool PDF strip failed: "+detail)
			var fallbackActions []string
			cleaned, fallbackActions := pdfStdlibDocumentStrip(data, opts)
			actions = append(actions, fallbackActions...)
			if err := os.WriteFile(source, cleaned, 0o600); err != nil {
				return nil, actions, err
			}
			exiftool = ""
		}
	} else {
		cleaned, fallbackActions := pdfStdlibDocumentStrip(data, opts)
		actions = append(actions, fallbackActions...)
		if err := os.WriteFile(source, cleaned, 0o600); err != nil {
			return nil, actions, err
		}
	}

	deepMode := opts.DeepImages
	markersLeft := func() bool {
		current, readErr := os.ReadFile(source)
		if readErr != nil {
			return false
		}
		return pdfMarkersPresentData(current)
	}

	settle := func(ran bool) {
		if !ran {
			return
		}
		if exiftool != "" {
			if ok, detail := runPDFExiftool(exiftool, source, deadline); ok {
				actions = append(actions, "exiftool post-deep metadata strip")
				pdfStructuralRewrite(source, &actions, deadline)
			} else {
				actions = append(actions, "warning: post-deep exiftool failed: "+detail)
				current, readErr := os.ReadFile(source)
				if readErr == nil {
					cleaned, fallbackActions := pdfStdlibDocumentStrip(current, opts)
					actions = append(actions, fallbackActions...)
					if writeErr := os.WriteFile(source, cleaned, 0o600); writeErr != nil {
						actions = append(actions, "warning: degraded PDF fallback write failed: "+writeErr.Error())
					}
				}
				exiftool = ""
			}
		} else if !opts.DisableExternalTools {
			actions = append(actions, "warning: the re-distill stamped its own /Producer and there is no exiftool to remove it")
		}
	}

	if deepMode != DeepNever && (deepMode == DeepAlways || deepMode == DeepLossless || markersLeft()) {
		if deepMode == DeepAuto {
			actions = append(actions, "residual PDF AI/C2PA markers after document-level strip (embedded image suspected); running deep image pass")
		}
		gs, gsErr := findGhostscript()
		if opts.DisableExternalTools {
			gsErr = os.ErrNotExist
		}
		if gsErr != nil {
			actions = append(actions, "warning: metadata inside embedded images left in place; install ghostscript for the deep image pass")
		} else if !deadline.spent() {
			deepOut := filepath.Join(tempDir, "deep-image.pdf")
			if ok, detail := runPDFGhostscript(gs, source, deepOut, false, deadline); ok {
				if err := os.Rename(deepOut, source); err != nil {
					return nil, actions, err
				}
				actions = append(actions, "ghostscript pdfwrite deep image pass, images passed through (rc=0)")
				settle(true)
				current, readErr := os.ReadFile(source)
				survived := false
				if readErr == nil && deepMode != DeepLossless {
					survived = markersLeft() || (deepMode == DeepAlways && embeddedImageMetadataPresent(current))
				}
				if survived {
					actions = append(actions, "metadata survived the lossless pass (it is inside the image stream); escalating to a re-encoding pass")
					reencodedPath := filepath.Join(tempDir, "reencoded.pdf")
					if ok, detail := runPDFGhostscript(gs, source, reencodedPath, true, deadline); ok {
						if err := os.Rename(reencodedPath, source); err != nil {
							return nil, actions, err
						}
						actions = append(actions, "ghostscript pdfwrite deep image pass, images re-encoded (rc=0)")
						settle(true)
					} else {
						actions = append(actions, "warning: residual embedded markers; lossy deep pass failed: "+detail)
					}
				}
			} else {
				actions = append(actions, "warning: ghostscript deep image pass failed: "+detail)
			}
		} else {
			actions = append(actions, "deep image pass skipped: clean budget exhausted")
		}
	} else if deepMode == DeepAuto {
		actions = append(actions, "deep image pass not needed; use deep_images=always to clear ordinary embedded EXIF")
	}
	final, err := os.ReadFile(source)
	if err != nil {
		return nil, actions, err
	}
	return final, actions, nil
}

func findGhostscript() (string, error) {
	for _, name := range []string{"gs", "gswin64c", "gswin32c"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func pdfStdlibDocumentStrip(data []byte, opts Options) ([]byte, []string) {
	out := append([]byte(nil), data...)
	blanked := 0
	stripAll := stripAllMetadata(opts)
	for _, packet := range pdfXMPPackets(data) {
		if !stripAll && len(containsAny(data[packet.openStart:packet.closeEnd], aiMetaHints)) == 0 {
			continue
		}
		blanked++
		for i := packet.openStart; i < packet.closeEnd; i++ {
			switch out[i] {
			case '\n', '\r', '\t':
			default:
				out[i] = ' '
			}
		}
	}
	if blanked > 0 {
		return out, []string{
			fmt.Sprintf("blanked XMP xpacket x%d (degraded; byte offsets preserved)", blanked),
			"warning: pure-stdlib PDF strip is best-effort; prefer exiftool",
		}
	}
	return out, []string{"no PDF cleaner available (install exiftool for reliable metadata strip); document-level metadata left as-is"}
}

func pdfJPEGSegments(blob []byte, start int, visit func(marker byte, payload []byte) bool) bool {
	i := start + 2
	limit := len(blob)
	for i+3 < limit {
		if blob[i] != 0xff {
			return false
		}
		for i+1 < limit && blob[i+1] == 0xff {
			i++
		}
		if i+1 >= limit {
			return false
		}
		marker := blob[i+1]
		if marker == 0xda || marker == 0xd9 {
			return false
		}
		if marker >= 0xd0 && marker <= 0xd8 {
			i += 2
			continue
		}
		if i+4 > limit {
			return false
		}
		length := int(binary.BigEndian.Uint16(blob[i+2 : i+4]))
		if length < 2 {
			return false
		}
		end := i + 2 + length
		payloadEnd := end
		if payloadEnd > limit {
			payloadEnd = limit
		}
		if payloadEnd < i+4 {
			return false
		}
		if visit(marker, blob[i+4:payloadEnd]) {
			return true
		}
		if end > limit {
			return false
		}
		i = end
	}
	return false
}

func pdfAnyJPEG(data []byte, predicate func([]byte, int) bool) bool {
	pos := 0
	for pos < len(data) {
		rel := bytes.Index(data[pos:], []byte{0xff, 0xd8, 0xff})
		if rel < 0 {
			return false
		}
		start := pos + rel
		if predicate(data, start) {
			return true
		}
		pos = start + 2
	}
	return false
}

func embeddedImageMetadataPresent(data []byte) bool {
	return pdfAnyJPEG(data, func(blob []byte, start int) bool {
		return pdfJPEGSegments(blob, start, func(marker byte, _ []byte) bool {
			return pdfJPEGMetadataMarkers[marker]
		})
	})
}

func embeddedProvenancePresent(data []byte) bool {
	return pdfAnyJPEG(data, func(blob []byte, start int) bool {
		return pdfJPEGSegments(blob, start, func(marker byte, payload []byte) bool {
			if !pdfJPEGProvenanceMarkers[marker] {
				return false
			}
			return len(containsAny(payload, pdfJPEGProvenanceSignatures)) > 0
		})
	})
}

func pdfMarkersPresentData(data []byte) bool {
	c2pa, ai, _, _ := inspectPDF(data)
	return c2pa || ai || embeddedProvenancePresent(data)
}

func runPDFProcess(path string, args []string, timeout time.Duration) (stdout, stderr string, returnCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var out, diagnostic limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), diagnostic.String(), -1, fmt.Errorf("timed out after %s", timeout)
	}
	returnCode = -1
	if cmd.ProcessState != nil {
		returnCode = cmd.ProcessState.ExitCode()
	}
	return out.String(), diagnostic.String(), returnCode, runErr
}

func pdfToolDetail(stdout, stderr string, err error) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if detail == "" && err != nil {
		detail = err.Error()
	}
	if detail == "" {
		detail = "no diagnostic output"
	}
	return truncate(detail, 2000)
}

func runPDFExiftool(path, source string, deadline *pdfDeadline) (bool, string) {
	if deadline != nil && deadline.spent() {
		return false, "clean budget exhausted"
	}
	stdout, stderr, returnCode, err := runPDFProcess(path, []string{"-all=", "-overwrite_original", source}, deadline.timeout(pdfExiftoolTimeout))
	if err != nil || returnCode != 0 {
		return false, fmt.Sprintf("exit %d: %s", returnCode, pdfToolDetail(stdout, stderr, err))
	}
	return true, fmt.Sprintf("rc=%d", returnCode)
}

func pdfStructuralRewrite(source string, actions *[]string, deadline *pdfDeadline) bool {
	qpdf, err := exec.LookPath("qpdf")
	if err != nil {
		*actions = append(*actions, "warning: exiftool PDF edits are incremental — the original metadata bytes remain recoverable; install qpdf for a structural rewrite")
		return false
	}
	if deadline != nil && deadline.spent() {
		*actions = append(*actions, "qpdf structural rewrite skipped: clean budget exhausted; metadata bytes may remain recoverable")
		return false
	}
	ok, detail := runPDFQPDF(qpdf, source, deadline)
	if ok {
		*actions = append(*actions, "qpdf --linearize structural rewrite ("+detail+")")
		return true
	}
	*actions = append(*actions, "qpdf rewrite skipped ("+detail+"); metadata bytes may remain recoverable")
	return false
}

func runPDFQPDF(path, source string, deadline *pdfDeadline) (bool, string) {
	dest := source + ".qpdf-tmp"
	defer os.Remove(dest)
	stdout, stderr, returnCode, err := runPDFProcess(path, []string{"--linearize", "--", source, dest}, deadline.timeout(pdfQPDFTimeout))
	st, statErr := os.Stat(dest)
	if (returnCode == 0 || returnCode == 3) && statErr == nil && st.Size() > 0 {
		if renameErr := os.Rename(dest, source); renameErr != nil {
			return false, renameErr.Error()
		}
		return true, fmt.Sprintf("rc=%d", returnCode)
	}
	if statErr != nil && returnCode >= 0 && err == nil {
		return false, fmt.Sprintf("rc=%d: qpdf produced no readable output", returnCode)
	}
	return false, fmt.Sprintf("rc=%d: %s", returnCode, pdfToolDetail(stdout, stderr, err))
}

func runPDFGhostscript(path, source, dest string, reencode bool, deadline *pdfDeadline) (bool, string) {
	args := []string{
		"-dBATCH", "-dNOPAUSE", "-dQUIET", "-dSAFER",
		"-sDEVICE=pdfwrite", "-dPDFSETTINGS=/prepress", "-dAutoRotatePages=/None",
		fmt.Sprintf("-dPassThroughJPEGImages=%t", !reencode),
		fmt.Sprintf("-dPassThroughJPXImages=%t", !reencode),
		"-dDownsampleColorImages=false", "-dDownsampleGrayImages=false", "-dDownsampleMonoImages=false",
		"-sOutputFile=" + dest, source,
	}
	stdout, stderr, returnCode, err := runPDFProcess(path, args, deadline.timeout(pdfToolTimeout))
	if err != nil || returnCode != 0 {
		return false, fmt.Sprintf("exit %d: %s", returnCode, pdfToolDetail(stdout, stderr, err))
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() == 0 {
		if err != nil {
			return false, err.Error()
		}
		return false, "Ghostscript produced an empty output"
	}
	return true, ""
}

func pdfMarkersPresent(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return pdfMarkersPresentData(data)
}

type limitedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 1 << 20
	if b.Len() < limit {
		remaining := limit - b.Len()
		if len(p) > remaining {
			_, _ = b.Buffer.Write(p[:remaining])
			b.truncated = true
			return len(p), nil
		}
	}
	if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}
