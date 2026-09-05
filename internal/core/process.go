package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func forcedKind(opts Options, path string, data []byte) (Kind, error) {
	if opts.ForceType != "" && opts.ForceType != "auto" {
		switch opts.ForceType {
		case "text":
			return KindText, nil
		case "image":
			return KindImage, nil
		case "container":
			return KindContainer, nil
		case "av", "audio", "video":
			return KindAV, nil
		default:
			return KindUnknown, fmt.Errorf("invalid forced type %q", opts.ForceType)
		}
	}
	kind, _ := Classify(path)
	if kind == KindUnknown {
		kind = classifyBytes(data, filepath.Ext(path))
	}
	if kind == KindUnknown && (opts.ForceText) {
		return KindText, nil
	}
	return kind, nil
}

func inspectData(data []byte, path string, kind Kind, opts Options) FileReport {
	report, _ := inspectDataWithExternalError(data, path, kind, opts, true)
	return report
}

func inspectDataWithExternal(data []byte, path string, kind Kind, opts Options, runExternal bool) FileReport {
	report, _ := inspectDataWithExternalError(data, path, kind, opts, runExternal)
	return report
}

func inspectDataWithExternalError(data []byte, path string, kind Kind, opts Options, runExternal bool) (FileReport, error) {
	if !runExternal {
		opts.DisableExternalTools = true
	}
	switch kind {
	case KindText:
		r := inspectText(data, opts.AggressiveHomoglyphs, opts.StripEmojiGlue)
		if opts.Stylometry {
			r.Stylometry = scoreStylometry(string(data), path, opts.Threshold)
		}
		return FileReport{Kind: KindText, Path: path, SuspiciousTotal: r.SuspiciousTotal, Text: &r}, nil
	case KindImage:
		return inspectImageWithOptions(data, path, opts, runExternal), nil
	case KindAV:
		return inspectAV(data, path), nil
	case KindContainer:
		return inspectContainerWithError(data, path, detectContainerFormat(path, data), opts)
	default:
		return FileReport{Kind: KindUnknown, Path: path, Notes: []string{"unrecognized format; pass --as text|image|container|av or --force-text to override"}}, nil
	}
}

func validateKind(data []byte, path string, opts Options) (Kind, error) {
	kind, err := forcedKind(opts, path, data)
	if err != nil {
		return KindUnknown, err
	}
	if kind == KindText && !opts.ForceText {
		if why := isBinary(data); why != "" {
			return KindUnknown, fmt.Errorf("refusing to treat %s as text: it looks like %s; use --force-text or --as", path, why)
		}
	}
	return kind, nil
}

// InspectBytes applies the same format detection and Layer A inspection used
// by InspectFile. It is also the byte-oriented API used by the HTTP service.
func InspectBytes(data []byte, path string, opts Options) (FileReport, error) {
	return inspectBytesWithExternal(data, path, opts, true)
}

// InspectBytesWithExternal is the byte-oriented inspection API with an
// explicit optional-tool policy. Remote website audits pass false so a local
// c2patool/exiftool cannot be invoked on downloaded content.
func InspectBytesWithExternal(data []byte, path string, opts Options, runExternal bool) (FileReport, error) {
	return inspectBytesWithExternal(data, path, opts, runExternal)
}

func inspectBytesWithExternal(data []byte, path string, opts Options, runExternal bool) (FileReport, error) {
	kind, err := validateKind(data, path, opts)
	if err != nil {
		return FileReport{}, err
	}
	return inspectDataWithExternalError(data, path, kind, opts, runExternal)
}

func cleanData(data []byte, path string, opts Options) (Kind, string, []byte, []string, map[string]any, error) {
	kind, err := forcedKind(opts, path, data)
	if err != nil {
		return KindUnknown, "", nil, nil, nil, err
	}
	if kind == KindUnknown && !opts.ForceText {
		return KindUnknown, "", nil, nil, nil, fmt.Errorf("refusing to classify %s: unrecognized format; pass --force-text or --as", path)
	}
	if kind == KindText && !opts.ForceText {
		if why := isBinary(data); why != "" {
			return KindUnknown, "", nil, nil, nil, fmt.Errorf("refusing to treat %s as text: it looks like %s; use --force-text or --as", path, why)
		}
	}
	cleaned := data
	actions := []string{}
	var stats map[string]any
	format := ""
	switch kind {
	case KindText:
		format = "text"
		cleaned, stats = cleanText(data, opts)
		if n, ok := stats["removed_count"].(int); ok && n > 0 {
			actions = append(actions, fmt.Sprintf("Layer A text: removed=%d", n))
		}
		if n, ok := stats["replaced_count"].(int); ok && n > 0 {
			actions = append(actions, fmt.Sprintf("Layer A text: replaced=%d", n))
		}
	case KindImage:
		format = detectImageFormat(data)
		cleaned, actions, err = cleanImage(data, format, opts)
		if err == nil {
			var exifActions []string
			cleaned, exifActions = imageExiftoolPass(cleaned, path, opts)
			actions = append(actions, exifActions...)
		}
	case KindAV:
		format = detectAVFormat(data)
		if format == "unknown" && opts.AudioRemix && isAudioRemixName(path) {
			// clean-audio deliberately accepts audio containers that the shared
			// metadata router does not parse (AAC/Ogg/Opus). Let ffmpeg own the
			// destructive transform instead of rejecting them before validation.
			format = "audio"
			cleaned = data
			actions = nil
		} else {
			cleaned, actions, err = cleanAV(data, format, opts)
		}
	case KindContainer:
		format = detectContainerFormat(path, data)
		cleaned, actions, err = cleanContainer(data, format, opts)
	}
	if err != nil {
		return KindUnknown, format, nil, actions, stats, err
	}
	return kind, format, cleaned, actions, stats, nil
}

// CleanBytes performs an in-memory clean and returns the cleaned bytes. It
// intentionally does not write a file, making it suitable for API clients and
// callers that need to choose their own destination.
func CleanBytes(data []byte, path string, opts Options) (CleanResult, []byte, error) {
	result, cleaned, _, err := cleanBytesWithReport(data, path, opts)
	return result, cleaned, err
}

// cleanBytesWithReport is the internal form used by the HTTP service. The
// post-clean report is returned so callers that need to add another optional
// pass (for example pixel purification) do not have to rerun the same image
// residual scan when no bytes changed after this stage.
func cleanBytesWithReport(data []byte, path string, opts Options) (CleanResult, []byte, FileReport, error) {
	if opts.RemovePixel != "" && opts.AudioRemix {
		return CleanResult{}, nil, FileReport{}, errors.New("--remove-pixel and --remix-audio cannot be combined")
	}
	kind, format, cleaned, actions, stats, err := cleanData(data, path, opts)
	if err != nil {
		return CleanResult{}, nil, FileReport{}, err
	}
	var synthIDBefore map[string]any
	if kind == KindImage {
		synthIDBefore = detectSynthIDForOptions(data, opts)
	}
	var pixelReport map[string]any
	pixelApplied := false
	if opts.RemovePixel != "" {
		if kind != KindImage && !(kind == KindAV && isVideoName(path)) {
			return CleanResult{}, nil, FileReport{}, errors.New("remove-pixel requires an image or video input")
		}
		tempDest, tempErr := os.CreateTemp("", "aiwr-pixel-output-*"+filepath.Ext(path))
		if tempErr != nil {
			return CleanResult{}, nil, FileReport{}, tempErr
		}
		tempPath := tempDest.Name()
		if closeErr := tempDest.Close(); closeErr != nil {
			_ = os.Remove(tempPath)
			return CleanResult{}, nil, FileReport{}, closeErr
		}
		defer os.Remove(tempPath)
		metadataCleaned := cleaned
		var pixelOutput []byte
		var pixelErr error
		pixelOutput, pixelReport, pixelErr = runPixelBackend(metadataCleaned, path, tempPath, 0o600, opts)
		if pixelErr != nil {
			if kind != KindImage || pixelReport == nil || pixelReport["available"] != false {
				return CleanResult{}, nil, FileReport{}, pixelErr
			}
			cleaned = metadataCleaned
			actions = append(actions, "pixel watermark removal skipped: "+pixelErrorMessage(pixelReport, pixelErr))
		} else {
			cleaned = pixelOutput
			pixelApplied = true
			actions = append(actions, "pixel watermark removal via "+opts.RemovePixel)
		}
	}
	changed := !sameBytes(data, cleaned)
	if len(actions) == 0 {
		actions = []string{"no metadata removed (already clean or none matched)"}
	}
	result := CleanResult{Kind: kind, Format: format, Input: path, Changed: changed, BytesIn: int64(len(data)), BytesOut: int64(len(cleaned)), Stats: stats, Actions: actions, PixelRemoval: pixelReport, SynthIDBefore: synthIDBefore}
	var finalReport FileReport
	var reportErr error
	if kind == KindImage {
		finalReport = inspectImageResidualWithOptions(cleaned, path, opts)
	} else {
		finalReport, reportErr = InspectBytes(cleaned, path, opts)
	}
	if reportErr != nil {
		return CleanResult{}, nil, FileReport{}, reportErr
	}
	applyPostReport(&result, finalReport, cleaned)
	if kind == KindImage {
		if pixelApplied {
			result.SynthIDAfter = detectSynthIDForOptions(cleaned, opts)
		} else {
			// A metadata-only clean does not alter pixels; preserve the
			// before score instead of presenting a second measurement as
			// if a pixel pass had run.
			result.SynthIDAfter = synthIDBefore
		}
	}
	return result, cleaned, finalReport, nil
}

func InspectFile(path string, opts Options) (FileReport, error) {
	data, _, err := readRegular(path)
	if err != nil {
		return FileReport{}, err
	}
	kind, err := validateKind(data, path, opts)
	if err != nil {
		return FileReport{}, err
	}
	return inspectDataWithExternalError(data, path, kind, opts, true)
}

func CleanFile(path string, opts Options) (CleanResult, error) {
	data, mode, err := readRegular(path)
	if err != nil {
		return CleanResult{}, err
	}
	kind, format, cleaned, actions, stats, err := cleanData(data, path, opts)
	if err != nil {
		return CleanResult{}, err
	}
	if opts.AudioRemix {
		if err := validateAudioRemixInput(path, kind); err != nil {
			return CleanResult{}, err
		}
	}
	var synthIDBefore map[string]any
	if kind == KindImage {
		synthIDBefore = detectSynthIDForOptions(data, opts)
	}
	var pixelReport map[string]any
	pixelApplied := false
	if opts.RemovePixel != "" {
		if kind != KindImage && !(kind == KindAV && isVideoName(path)) {
			return CleanResult{}, errors.New("remove-pixel requires an image or video input")
		}
		if opts.AudioRemix {
			return CleanResult{}, errors.New("--remove-pixel and --remix-audio cannot be combined")
		}
	}
	dest := opts.Output
	if opts.InPlace {
		bak, created, e := backupFile(path, opts.Reflink)
		if e != nil {
			return CleanResult{}, e
		}
		if !created && !opts.Quiet {
			fmt.Fprintf(os.Stderr, "backup %s already exists; keeping the original backup\n", bak)
		}
		dest = path
	}
	if dest == "" {
		dest = cleanedPath(path)
	}
	if !opts.InPlace {
		if abs, e := filepath.Abs(dest); e == nil {
			dest = abs
		}
	}
	if opts.RemovePixel != "" {
		metadataCleaned := cleaned
		var pixelOutput []byte
		pixelOutput, pixelReport, err = runPixelBackend(metadataCleaned, path, dest, mode, opts)
		if err != nil {
			if kind != KindImage || pixelReport == nil || pixelReport["available"] != false {
				return CleanResult{}, err
			}
			cleaned = metadataCleaned
			actions = append(actions, "pixel watermark removal skipped: "+pixelErrorMessage(pixelReport, err))
		} else {
			cleaned = pixelOutput
			pixelApplied = true
			actions = append(actions, "pixel watermark removal via "+opts.RemovePixel)
		}
	}
	changed := !sameBytes(data, cleaned)
	if dest == path && !changed {
		cleaned = data
	}
	writeOutput := !(opts.OnlyChanged && !changed && !opts.InPlace && !opts.AudioRemix && opts.RemovePixel == "")
	if changed && writeOutput {
		if err = writeAtomic(dest, cleaned, mode); err != nil {
			return CleanResult{}, err
		}
	} else if dest != path && writeOutput {
		_, err = copyFile(path, dest, mode, opts.Reflink)
		if err != nil {
			return CleanResult{}, err
		}
	}
	if opts.AudioRemix && kind == KindAV {
		if dest != path && !changed {
			if _, err = copyFile(path, dest, mode, opts.Reflink); err != nil {
				return CleanResult{}, err
			}
		}
		if err := runAudioRemix(dest, dest, opts); err != nil {
			return CleanResult{}, err
		}
		actions = append(actions, "destructive audio remix via ffmpeg")
		changed = true
	}
	if !writeOutput && !opts.AudioRemix {
		dest = ""
	}
	st, statErr := os.Stat(dest)
	bytesOut := int64(len(cleaned))
	if statErr == nil {
		bytesOut = st.Size()
	}
	result := CleanResult{Kind: kind, Format: format, Input: path, Output: dest, Changed: changed, BytesIn: int64(len(data)), BytesOut: bytesOut, Stats: stats, Actions: actions, PixelRemoval: pixelReport}
	if len(actions) == 0 {
		result.Actions = []string{"no metadata removed (already clean or none matched)"}
	}
	final := cleaned
	var readErr error
	if dest != "" {
		final, readErr = os.ReadFile(dest)
	}
	if readErr == nil {
		reportPath := dest
		if reportPath == "" {
			reportPath = path
		}
		switch kind {
		case KindImage:
			r := inspectImageResidualWithOptions(final, reportPath, opts)
			applyPostReport(&result, r, final)
			result.SynthIDBefore = synthIDBefore
			if pixelApplied {
				result.SynthIDAfter = detectSynthIDForOptions(final, opts)
			} else {
				result.SynthIDAfter = synthIDBefore
			}
		case KindAV:
			r := inspectAV(final, reportPath)
			applyPostReport(&result, r, final)
		case KindContainer:
			r, reportErr := inspectContainerWithError(final, reportPath, detectContainerFormat(reportPath, final), opts)
			if reportErr != nil {
				return result, reportErr
			}
			applyPostReport(&result, r, final)
		}
	}
	return result, nil
}

func isAudioRemixName(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav", ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus":
		return true
	default:
		return false
	}
}

func validateAudioRemixInput(path string, kind Kind) error {
	if kind != KindAV {
		return errors.New("--remix-audio requires an audio input")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav", ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus":
		// These are the only inputs that may safely pass through the -vn
		// audio-only remix pipeline. MP4/MOV/M4V could silently lose video.
	default:
		return fmt.Errorf("--remix-audio is only supported for WAV, MP3, FLAC, M4A, AAC, OGG, and Opus; refusing video input %s", path)
	}
	hasVideo, err := mediaHasVideo(path)
	if err != nil {
		return err
	}
	if hasVideo {
		return fmt.Errorf("--remix-audio refuses media with a video stream: %s", path)
	}
	return nil
}

func applyPostReport(result *CleanResult, report FileReport, data []byte) {
	result.StillHasC2PA = report.HasC2PA
	result.StillHasAI = report.HasAIMetadata
	result.PostFindings = append([]string(nil), report.Findings...)
	result.Partial = reportIsPartial(report)
	// A parsed MP4 with an unparseable top-level tail is deliberately kept
	// byte-for-byte, but the untouched tail means the post-clean inspection is
	// incomplete. Match upstream's conservative residual contract.
	if report.Kind == KindAV && strings.EqualFold(report.Format, "mp4") && isTruncatedISOBMFF(data) {
		const finding = "MP4 not fully inspected: preserved a truncated top-level box tail"
		if !containsString(result.PostFindings, finding) {
			result.PostFindings = append(result.PostFindings, finding)
		}
		result.StillHasAI = true
		result.Partial = true
	}
}

func reportIsPartial(report FileReport) bool {
	for _, value := range report.Findings {
		low := strings.ToLower(value)
		if strings.Contains(low, "partial read") || strings.Contains(low, "not fully inspected") || strings.Contains(low, "truncated") {
			return true
		}
	}
	for _, value := range report.Notes {
		low := strings.ToLower(value)
		if strings.Contains(low, "format not fully inspected") && !strings.Contains(low, "by this tool") {
			return true
		}
	}
	return false
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func ensureDirMode(path string, mode fs.FileMode) error {
	if err := ensureNoSymlinkComponents(filepath.Dir(path)); err != nil {
		return err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlink as output directory: %s", path)
		}
		if !st.IsDir() {
			return fmt.Errorf("output path is not a directory: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, mode.Perm()); err != nil {
		return err
	}
	return os.Chmod(path, mode.Perm())
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || !(rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))))
}

func CleanDirectory(path string, opts Options) (BatchSummary, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return BatchSummary{}, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return BatchSummary{}, err
	}
	if !st.IsDir() {
		return BatchSummary{}, fmt.Errorf("not a directory: %s", path)
	}
	dest := opts.OutputDir
	if dest == "" {
		dest = directoryCleanedPath(root)
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return BatchSummary{}, err
	}
	if same, _ := filepath.Abs(root); same == dest {
		return BatchSummary{}, errors.New("output directory must differ from input directory")
	}
	if pathWithin(root, dest) {
		return BatchSummary{}, fmt.Errorf("output directory must not be inside input directory: %s", dest)
	}
	if err := ensureNoSymlinkComponents(filepath.Dir(dest)); err != nil {
		return BatchSummary{}, err
	}
	if err := ensureDirMode(dest, st.Mode()); err != nil {
		return BatchSummary{}, err
	}
	summary := BatchSummary{Input: root, Output: dest, Items: []BatchItem{}}
	var walkErr error
	walkErr = filepath.WalkDir(root, func(current string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, current)
		if rel == "." {
			return nil
		}
		outPath := filepath.Join(dest, rel)
		info, e := d.Info()
		if e != nil {
			return e
		}
		if d.IsDir() {
			return ensureDirMode(outPath, info.Mode())
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(current)
			if e != nil {
				return e
			}
			if err := ensureNoSymlink(outPath); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(target, outPath); err != nil {
				return err
			}
			summary.Files++
			summary.Items = append(summary.Items, BatchItem{Path: current, Output: outPath})
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		local := opts
		local.Output = outPath
		local.OutputDir = ""
		local.InPlace = false
		local.JSON = false
		r, e := CleanFile(current, local)
		if e != nil {
			kind, _ := Classify(current)
			if kind == KindUnknown && !opts.ForceText && (opts.ForceType == "" || opts.ForceType == "auto") {
				if opts.SkipUnknown || opts.OnlyChanged {
					summary.Files++
					summary.Items = append(summary.Items, BatchItem{Path: current, Output: outPath, Error: "unknown format skipped"})
					return nil
				}
				_, copyErr := copyFile(current, outPath, info.Mode().Perm(), opts.Reflink)
				if copyErr != nil {
					summary.Errors++
					summary.Items = append(summary.Items, BatchItem{Path: current, Output: outPath, Error: copyErr.Error()})
					return nil
				}
				summary.Files++
				summary.Items = append(summary.Items, BatchItem{Path: current, Output: outPath, Error: "unknown format copied unchanged"})
				return nil
			}
			summary.Errors++
			summary.Items = append(summary.Items, BatchItem{Path: current, Output: outPath, Error: e.Error()})
			return nil
		}
		summary.Files++
		if r.Changed {
			summary.Changed++
		}
		summary.Items = append(summary.Items, BatchItem{Path: current, Output: outPath, Result: &r})
		return nil
	})
	if walkErr != nil {
		return summary, walkErr
	}
	sort.Slice(summary.Items, func(i, j int) bool { return summary.Items[i].Path < summary.Items[j].Path })
	return summary, nil
}

func InspectDirectory(path string, opts Options) ([]FileReport, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", path)
	}
	reports := []FileReport{}
	walkErr := filepath.WalkDir(root, func(current string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if info, er := d.Info(); er == nil && info.Mode().IsRegular() {
			r, er := InspectFile(current, opts)
			if er != nil {
				reports = append(reports, FileReport{Path: current, Kind: KindUnknown, Notes: []string{er.Error()}})
			} else {
				reports = append(reports, r)
			}
		}
		return nil
	})
	sort.Slice(reports, func(i, j int) bool { return reports[i].Path < reports[j].Path })
	return reports, walkErr
}

func Marshal(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
