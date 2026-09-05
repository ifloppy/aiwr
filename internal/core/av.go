package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func inspectAV(data []byte, path string) FileReport {
	fmtName := detectAVFormat(data)
	var c2pa, ai bool
	var findings []string
	switch fmtName {
	case "mp4":
		c2pa, ai, findings = inspectISOBMFF(data, fmtName)
		c2, a, more := inspectMP4UDTA(data)
		c2pa, ai = c2pa || c2, ai || a
		findings = append(findings, more...)
	case "wav":
		c2pa, ai, findings = inspectWAV(data)
	case "mp3":
		c2pa, ai, findings = inspectID3(data)
	case "flac":
		c2pa, ai, findings = inspectFLAC(data)
	default:
		findings = []string{"unsupported format (MP4/MOV/M4A/WAV/MP3/FLAC)"}
	}
	report := FileReport{Kind: KindAV, Path: path, Format: fmtName, HasC2PA: c2pa, HasAIMetadata: ai, Findings: findings}
	for _, f := range findings {
		report.FindingsConfidence = append(report.FindingsConfidence, findingConfidence(f))
	}
	if fmtName == "unknown" {
		report.Notes = []string{"format not fully inspected; only MP4/MOV/M4A/WAV/MP3/FLAC are supported"}
	}
	return report
}

func isTruncatedISOBMFF(data []byte) bool {
	boxes, end := parseBMFF(data, 0, len(data))
	return len(boxes) > 0 && len(data)-end >= 8
}

func cleanAV(data []byte, format string, opts Options) ([]byte, []string, error) {
	stripAll := stripAllMetadata(opts)
	switch format {
	case "mp4":
		return stripMP4(data, stripAll)
	case "wav":
		return stripWAV(data, stripAll)
	case "mp3":
		return stripID3(data, stripAll)
	case "flac":
		return stripFLAC(data, stripAll)
	default:
		return data, nil, fmt.Errorf("unsupported audio/video format: %s", format)
	}
}

func detectAVFormat(data []byte) string {
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		// The upstream AV router treats every ftyp file as MP4-family media.
		// AVIF/HEIC are selected by the image pipeline first (their extensions
		// and content sniffing win in format dispatch), but an ftyp file whose
		// caller explicitly routes it through AV must retain this behavior.
		return "mp4"
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")) {
		return "wav"
	}
	if bytes.HasPrefix(data, []byte("fLaC")) {
		return "flac"
	}
	if len(data) >= 10 && bytes.Equal(data[:3], []byte("ID3")) {
		if total, _, _, ok := parseID3(data); ok && total < len(data) && bytes.HasPrefix(data[total:], []byte("fLaC")) {
			return "flac"
		}
		return "mp3"
	}
	if len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 {
		return "mp3"
	}
	return "unknown"
}

func isVideoName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".mov", ".m4v":
		return true
	default:
		return false
	}
}

func mediaHasVideo(name string) (bool, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return false, fmt.Errorf("ffprobe is required to determine whether %s contains a video stream: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout, stderr limitedBuffer
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-show_entries", "stream=codec_type",
		"-of", "csv=p=0",
		name,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return false, errors.New("ffprobe timed out")
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return false, fmt.Errorf("ffprobe failed: %s", message)
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "video") {
			return true, nil
		}
	}
	return false, nil
}

func audioSampleRate(name string) int {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return 44100
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout limitedBuffer
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=sample_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		name,
	)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 44100
	}
	rate, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil || rate <= 0 {
		return 44100
	}
	return rate
}

func inspectMP4UDTA(data []byte) (bool, bool, []string) {
	boxes, _ := parseBMFF(data, 0, len(data))
	var c2pa, ai bool
	findings := []string{}
	for _, b := range boxes {
		if !b.typEqual("moov") {
			continue
		}
		subs, _ := parseBMFF(b.payload, 0, len(b.payload))
		for _, s := range subs {
			if !s.typEqual("udta") {
				continue
			}
			hits := containsAny(s.payload, aiMetaHints)
			if len(hits) > 0 {
				ai = true
				if isC2PAHit(hits) {
					c2pa = true
				}
				findings = append(findings, "MP4 moov/udta box: "+strings.Join(hits[:minInt(8, len(hits))], ", "))
			}
		}
	}
	return c2pa, ai, findings
}

func stripMP4(data []byte, stripAll bool) ([]byte, []string, error) {
	first, actions, err := stripISOBMFF(data, "mp4", stripAll)
	if err != nil {
		return data, actions, err
	}
	boxes, end := parseBMFF(first, 0, len(first))
	out := []byte{}
	for _, b := range boxes {
		if !b.typEqual("moov") {
			out = append(out, buildBMFFBox(b.typ, b.payload, b.header)...)
			continue
		}
		subs, scanned := parseBMFF(b.payload, 0, len(b.payload))
		moov := []byte{}
		for _, s := range subs {
			drop := s.typEqual("udta") && (stripAll || len(containsAny(s.payload, aiMetaHints)) > 0)
			if drop {
				actions = append(actions, "drop moov/udta box (generator/user-data tags)")
				moov = append(moov, freeBMFFBox(s.total, s.header)...)
			} else {
				moov = append(moov, buildBMFFBox(s.typ, s.payload, s.header)...)
			}
		}
		if scanned < len(b.payload) {
			moov = append(moov, b.payload[scanned:]...)
		}
		out = append(out, buildBMFFBox(b.typ, moov, b.header)...)
	}
	if end < len(first) {
		out = append(out, first[end:]...)
	}
	if len(actions) == 0 {
		actions = []string{"no MP4 metadata boxes removed (already clean or none matched)"}
	}
	return out, actions, nil
}

func inspectWAV(data []byte) (bool, bool, []string) {
	if len(data) < 12 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return false, false, []string{"not a WAV"}
	}
	findings := []string{}
	var c2pa, ai bool
	for pos := 12; pos+8 <= len(data); {
		id := data[pos : pos+4]
		sz := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		start, end := pos+8, pos+8+sz
		if end > len(data) {
			break
		}
		payload := data[start:end]
		switch {
		case bytes.Equal(id, []byte("C2PA")):
			c2pa = true
			findings = append(findings, "WAV C2PA-related manifest chunk")
		case bytes.Equal(id, []byte("LIST")) && len(payload) >= 4 && bytes.Equal(payload[:4], []byte("INFO")):
			hits := containsAny(payload, aiMetaHints)
			if len(hits) > 0 {
				ai = true
				if isC2PAHit(hits) {
					c2pa = true
				}
				findings = append(findings, "WAV LIST INFO chunk: "+strings.Join(hits[:minInt(8, len(hits))], ", "))
			}
		case strings.EqualFold(string(id), "id3 "):
			c, a, more := inspectID3(payload)
			if a {
				ai = true
				c2pa = c2pa || c
				for _, f := range more {
					findings = append(findings, "WAV id3 chunk / "+f)
				}
			}
		}
		pos = end + (sz & 1)
	}
	return c2pa, ai, findings
}

func stripWAV(data []byte, stripAll bool) ([]byte, []string, error) {
	if len(data) < 12 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return data, nil, fmt.Errorf("not a WAV")
	}
	out := append([]byte(nil), data[:12]...)
	actions := []string{}
	for pos := 12; pos+8 <= len(data); {
		id := data[pos : pos+4]
		sz := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		start, end := pos+8, pos+8+sz
		if end > len(data) {
			out = append(out, data[pos:]...)
			actions = append(actions, "kept truncated WAV tail")
			break
		}
		payload := data[start:end]
		drop, label := false, ""
		switch {
		case bytes.Equal(id, []byte("C2PA")):
			drop, label = true, "C2PA"
		case bytes.Equal(id, []byte("LIST")) && len(payload) >= 4 && bytes.Equal(payload[:4], []byte("INFO")) && (stripAll || len(containsAny(payload, aiMetaHints)) > 0):
			drop, label = true, "LIST INFO"
		case strings.EqualFold(string(id), "id3 ") && (stripAll || len(containsAny(payload, aiMetaHints)) > 0):
			drop, label = true, "id3"
		}
		if drop {
			actions = append(actions, "drop WAV "+label+" chunk")
		} else {
			out = append(out, data[pos:end+(sz&1)]...)
		}
		pos = end + (sz & 1)
	}
	if len(out) >= 8 {
		binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	}
	if len(actions) == 0 {
		actions = append(actions, "no WAV metadata chunks removed (already clean or none matched)")
	}
	return out, actions, nil
}

func synchsafe(b []byte) int {
	if len(b) < 4 {
		return 0
	}
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}
func synchsafeBytes(n int) []byte {
	return []byte{byte(n>>21) & 0x7f, byte(n>>14) & 0x7f, byte(n>>7) & 0x7f, byte(n) & 0x7f}
}

type id3Frame struct {
	id      []byte
	payload []byte
}

func parseID3(data []byte) (int, int, []id3Frame, bool) {
	if len(data) < 10 || !bytes.Equal(data[:3], []byte("ID3")) {
		return 0, 0, nil, false
	}
	major := int(data[3])
	size := synchsafe(data[6:10])
	total := 10 + size
	footer := 0
	if major == 4 && data[5]&0x10 != 0 {
		footer = 10
	}
	total += footer
	if total > len(data) {
		return total, major, nil, false
	}
	framesEnd := 10 + size
	if footer > 0 {
		expected := append([]byte("3DI"), data[3:10]...)
		if !bytes.Equal(data[framesEnd:total], expected) {
			return total, major, nil, false
		}
	}
	pos := 10
	if data[5]&0x40 != 0 {
		if pos+4 > framesEnd {
			return total, major, nil, false
		}
		if major == 4 {
			pos += synchsafe(data[pos : pos+4])
		} else {
			pos += int(binary.BigEndian.Uint32(data[pos:pos+4])) + 4
		}
		if pos > framesEnd {
			return total, major, nil, false
		}
	}
	if major < 3 {
		return total, major, nil, true
	}
	frames := []id3Frame{}
	for pos+10 <= framesEnd {
		id := data[pos : pos+4]
		if bytes.Equal(id, []byte{0, 0, 0, 0}) {
			break
		}
		fsz := 0
		if major == 4 {
			fsz = synchsafe(data[pos+4 : pos+8])
		} else {
			fsz = int(binary.BigEndian.Uint32(data[pos+4 : pos+8]))
		}
		end := pos + 10 + fsz
		if end > framesEnd {
			return total, major, nil, false
		}
		frames = append(frames, id3Frame{append([]byte(nil), id...), data[pos+10 : end]})
		pos = end
	}
	for _, b := range data[pos:framesEnd] {
		if b != 0 {
			return total, major, nil, false
		}
	}
	return total, major, frames, true
}

func inspectID3(data []byte) (bool, bool, []string) {
	if len(data) >= 10 && bytes.Equal(data[:3], []byte("ID3")) {
		total, major, frames, ok := parseID3(data)
		if !ok {
			declaredTotal := 10 + synchsafe(data[6:10])
			hits := containsAny(data, aiMetaHints)
			findings := []string{fmt.Sprintf("truncated ID3v2.%d tag detected (%d bytes present, %d declared) — metadata may be incomplete", major, len(data), declaredTotal)}
			if hits := containsAny(data, aiMetaHints); len(hits) > 0 {
				findings = append(findings, fmt.Sprintf("partial ID3v2.%d tag markers: %s", major, strings.Join(hits[:minInt(8, len(hits))], ", ")))
			}
			if declaredTotal <= len(data) {
				return false, false, nil
			}
			return isC2PAHit(hits), len(hits) > 0, findings
		}
		if len(frames) == 0 {
			hits := containsAny(data[:total], aiMetaHints)
			if len(hits) > 0 {
				return isC2PAHit(hits), true, []string{fmt.Sprintf("ID3v2.%d tag: %s", major, strings.Join(hits[:minInt(8, len(hits))], ", "))}
			}
			return false, false, nil
		}
		var c2pa, ai bool
		findings := []string{}
		for _, f := range frames {
			hits := containsAny(f.payload, aiMetaHints)
			if len(hits) > 0 {
				ai = true
				if isC2PAHit(hits) {
					c2pa = true
				}
				findings = append(findings, "ID3v2 frame "+string(f.id)+": "+strings.Join(hits[:minInt(8, len(hits))], ", "))
			}
		}
		return c2pa, ai, findings
	}
	return false, false, nil
}

func stripID3(data []byte, stripAll bool) ([]byte, []string, error) {
	if len(data) < 10 || !bytes.Equal(data[:3], []byte("ID3")) {
		return data, []string{"no ID3v2 tag removed (already clean or none matched)"}, nil
	}
	total, major, frames, ok := parseID3(data)
	if !ok {
		// A complete but malformed tag is kept verbatim. Only an ID3 header
		// whose declared body runs past EOF is treated as a truncated prefix
		// for the recovery scan below.
		declaredTotal := 10 + synchsafe(data[6:10])
		if declaredTotal <= len(data) {
			return data, nil, nil
		}
		for i := 10; i+4 < len(data); i++ {
			if isValidMP3FrameHeader(data, i) {
				return data[i:], []string{fmt.Sprintf("drop truncated ID3v2.%d tag (found audio frame at offset %d)", major, i)}, nil
			}
		}
		return data, []string{fmt.Sprintf("cannot locate valid audio frame in truncated ID3v2.%d tag; preserving file", major)}, nil
	}
	rest := data[total:]
	if len(frames) == 0 {
		if !stripAll && len(containsAny(data[:total], aiMetaHints)) == 0 {
			return data, []string{"no ID3v2 tag removed (no AI/C2PA markers found)"}, nil
		}
		return rest, []string{fmt.Sprintf("drop ID3v2.%d tag (%d bytes)", major, total)}, nil
	}
	if stripAll {
		return rest, []string{fmt.Sprintf("drop ID3v2.%d tag (%d bytes)", major, total)}, nil
	}
	kept := []byte{}
	actions := []string{}
	for _, f := range frames {
		hits := containsAny(f.payload, aiMetaHints)
		if len(hits) > 0 {
			actions = append(actions, "drop ID3v2 frame "+string(f.id)+": "+strings.Join(hits[:minInt(8, len(hits))], ", "))
			continue
		}
		kept = append(kept, f.id...)
		if major == 4 {
			kept = append(kept, synchsafeBytes(len(f.payload))...)
		} else {
			var n [4]byte
			binary.BigEndian.PutUint32(n[:], uint32(len(f.payload)))
			kept = append(kept, n[:]...)
		}
		kept = append(kept, 0, 0)
		kept = append(kept, f.payload...)
	}
	if len(actions) == 0 {
		return data, []string{"no ID3v2 frames removed (already clean or none matched)"}, nil
	}
	header := append([]byte("ID3"), byte(major), byte(0), byte(0))
	header = append(header, synchsafeBytes(len(kept))...)
	return append(append(header, kept...), rest...), actions, nil
}

func isValidMP3FrameHeader(data []byte, offset int) bool {
	if offset < 0 || offset+4 > len(data) {
		return false
	}
	b0, b1, b2, b3 := data[offset], data[offset+1], data[offset+2], data[offset+3]
	if b0 != 0xff || b1&0xe0 != 0xe0 {
		return false
	}
	version := (b1 >> 3) & 0x03
	layer := (b1 >> 1) & 0x03
	if version == 1 || layer == 0 {
		return false
	}
	bitrateIndex := (b2 >> 4) & 0x0f
	if bitrateIndex == 0 || bitrateIndex == 0x0f {
		return false
	}
	if (b2>>2)&0x03 == 0x03 {
		return false
	}
	return b3&0x03 != 0x02
}

func geobTextEnd(payload []byte, start int, encoding byte) int {
	terminator := []byte{0}
	if encoding == 1 || encoding == 2 {
		terminator = []byte{0, 0}
	}
	step := len(terminator)
	for pos := start; pos+step <= len(payload); pos += step {
		if bytes.Equal(payload[pos:pos+step], terminator) {
			return pos + step
		}
	}
	return -1
}

func isC2PAGeob(frame id3Frame) bool {
	if !bytes.Equal(frame.id, []byte("GEOB")) || len(frame.payload) == 0 || frame.payload[0] > 3 {
		return false
	}
	encoding := frame.payload[0]
	mimeEnd := bytes.IndexByte(frame.payload[1:], 0)
	if mimeEnd < 0 {
		return false
	}
	mimeEnd++
	if !bytes.Equal(bytes.ToLower(frame.payload[1:mimeEnd]), []byte("application/c2pa")) {
		return false
	}
	filenameEnd := geobTextEnd(frame.payload, mimeEnd+1, encoding)
	if filenameEnd < 0 {
		return false
	}
	descriptionEnd := geobTextEnd(frame.payload, filenameEnd, encoding)
	return descriptionEnd >= 0 && descriptionEnd < len(frame.payload)
}

func inspectFLAC(data []byte) (bool, bool, []string) {
	if bytes.HasPrefix(data, []byte("ID3")) {
		total, _, frames, ok := parseID3(data)
		if !ok || total >= len(data) || !bytes.HasPrefix(data[total:], []byte("fLaC")) {
			return false, false, nil
		}
		for _, f := range frames {
			if isC2PAGeob(f) {
				return true, true, []string{"C2PA-related manifest in ID3v2 frame GEOB: application/c2pa"}
			}
		}
		return false, false, nil
	}
	return false, false, nil
}
func stripFLAC(data []byte, stripAll bool) ([]byte, []string, error) {
	if !bytes.HasPrefix(data, []byte("ID3")) {
		return data, []string{"no FLAC ID3v2 metadata removed (already clean or none matched)"}, nil
	}
	total, major, frames, ok := parseID3(data)
	if !ok || total > len(data) || !bytes.HasPrefix(data[total:], []byte("fLaC")) {
		return data, []string{"no FLAC ID3v2 metadata removed (already clean or none matched)"}, nil
	}
	if stripAll {
		return data[total:], []string{fmt.Sprintf("drop FLAC ID3v2.%d tag (%d bytes)", major, total)}, nil
	}
	kept := []byte{}
	actions := []string{}
	pos := 10
	if data[5]&0x40 != 0 {
		if major == 4 {
			pos += synchsafe(data[10:14])
		} else {
			pos += int(binary.BigEndian.Uint32(data[10:14])) + 4
		}
	}
	for _, f := range frames {
		frameEnd := pos + 10 + len(f.payload)
		if isC2PAGeob(f) {
			actions = append(actions, "drop FLAC ID3v2 frame GEOB: application/c2pa")
		} else {
			kept = append(kept, data[pos:frameEnd]...)
		}
		pos = frameEnd
	}
	if len(actions) == 0 {
		return data, []string{"no FLAC ID3v2 frames removed (already clean or none matched)"}, nil
	}
	if len(kept) == 0 {
		return data[total:], actions, nil
	}
	h := append([]byte(nil), data[:5]...)
	h = append(h, data[5]&^0x50)
	h = append(h, synchsafeBytes(len(kept))...)
	return append(h, append(kept, data[total:]...)...), actions, nil
}

func runAudioRemix(src, dst string, opts Options) error {
	if math.IsNaN(opts.AudioTempo) || math.IsInf(opts.AudioTempo, 0) ||
		opts.AudioTempo < 0.5 || opts.AudioTempo > 2 ||
		opts.AudioTempo <= 1.05 && opts.AudioTempo >= 0.95 {
		return fmt.Errorf("tempo must be in [0.5, 2.0] and change must exceed +/-5%%")
	}
	if math.IsNaN(opts.AudioPitch) || math.IsInf(opts.AudioPitch, 0) ||
		opts.AudioPitch <= 1 && opts.AudioPitch >= -1 {
		return fmt.Errorf("pitch shift must exceed +/-1 semitone")
	}
	if opts.AudioBitrate == "" {
		opts.AudioBitrate = "96k"
	}
	if bitrate := formatBitrateKbps(opts.AudioBitrate); bitrate <= 0 {
		return fmt.Errorf("re-encode bitrate must be a positive value such as 96k")
	} else if bitrate >= 128 {
		return fmt.Errorf("lossy re-encode bitrate must be below 128 kbps")
	}
	cmdPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is required for --remix-audio: %w", err)
	}
	codec := strings.TrimSpace(opts.AudioCodec)
	ext := strings.ToLower(filepath.Ext(dst))
	if codec == "" {
		switch ext {
		case ".m4a", ".mp4":
			codec = "aac"
		case ".ogg", ".opus":
			codec = "libopus"
		case ".mp3":
			codec = "libmp3lame"
		case ".wav":
			codec = "pcm_s16le"
		case ".flac":
			codec = "flac"
		default:
			codec = "aac"
		}
	}
	if err := ensureNoSymlinkComponents(filepath.Dir(dst)); err != nil {
		return err
	}
	if err := ensureNoSymlinkComponents(filepath.Dir(src)); err != nil {
		return err
	}
	if err := ensureNoSymlink(dst); err != nil {
		return err
	}
	if err := ensureNoSymlink(src); err != nil {
		return err
	}
	outputExt := filepath.Ext(dst)
	tempPattern := "." + strings.TrimSuffix(filepath.Base(dst), outputExt) + "-*.aiwr-remix" + outputExt
	tempFile, err := os.CreateTemp(filepath.Dir(dst), tempPattern)
	if err != nil {
		return err
	}
	temp := tempFile.Name()
	if closeErr := tempFile.Close(); closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	defer os.Remove(temp)
	if st, statErr := os.Stat(dst); statErr == nil {
		if err := os.Chmod(temp, st.Mode().Perm()); err != nil {
			return err
		}
	}
	sampleRate := audioSampleRate(src)
	pitchFactor := math.Pow(2, opts.AudioPitch/12)
	filter := fmt.Sprintf("atempo=%g,asetrate=%d,aresample=%d,highpass=f=35,lowpass=f=14000,treble=g=-8", opts.AudioTempo, int(float64(sampleRate)*pitchFactor), sampleRate)
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", src, "-vn", "-af", filter, "-c:a", codec}
	if codec != "pcm_s16le" && codec != "flac" && codec != "copy" {
		args = append(args, "-b:a", opts.AudioBitrate)
	}
	args = append(args, temp, "-nostdin")
	seconds := opts.FFmpegTimeoutSeconds
	if seconds <= 0 {
		seconds = 1800
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg remix timed out after %ds", seconds)
		}
		return fmt.Errorf("ffmpeg remix failed: %s", strings.TrimSpace(string(output)))
	}
	if err := os.Rename(temp, dst); err != nil {
		return err
	}
	return nil
}
func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func formatBitrateKbps(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[len(s)-1] != 'k' || s[0] < '1' || s[0] > '9' {
		return 0
	}
	for i := 1; i < len(s)-1; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0
	}
	return n
}
