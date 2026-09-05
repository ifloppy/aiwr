package core

import (
	"encoding/json"
	"fmt"
)

// Kind is the processing pipeline selected for an input.
type Kind string

const (
	KindText      Kind = "text"
	KindImage     Kind = "image"
	KindContainer Kind = "container"
	KindAV        Kind = "av"
	KindUnknown   Kind = "unknown"
)

type DeepImages string

const (
	DeepAuto     DeepImages = "auto"
	DeepAlways   DeepImages = "always"
	DeepLossless DeepImages = "lossless"
	DeepNever    DeepImages = "never"
)

type ReflinkMode string

const (
	ReflinkAuto   ReflinkMode = "auto"
	ReflinkAlways ReflinkMode = "always"
	ReflinkNever  ReflinkMode = "never"
)

// Options deliberately mirrors the upstream script flags while adding
// directory and operational controls that make the Go CLI useful in pipes and
// batch jobs.
type Options struct {
	NFKC                   bool
	AggressiveHomoglyphs   bool
	NormalizeSpaces        bool
	StripEmojiGlue         bool
	StripBidi              bool
	Stylometry             bool
	Threshold              float64
	KeepNonAIMetadata      bool
	Strategy               string
	RewriteBackend         string
	RewriteAPIKey          string
	RewriteModel           string
	RewriteBaseURL         string
	RewriteStyle           string
	RewriteReasoningEffort string
	RewriteGumbelKey       string
	RewriteTemperature     float64
	RewriteTimeoutSeconds  int
	RewriteAllowRemote     bool
	RewriteTargetMargin    float64
	RewriteChunkShuffle    bool
	RewriteNoopLexFloor    float64
	LayerAAfter            bool
	LayerAOnly             bool
	AlsoLayerAText         bool
	DetectBefore           bool
	DetectAfter            bool
	StripAllMetadata       bool
	StripAllMetadataSet    bool
	RemovePixel            string
	RemoveAudioWatermark   bool
	SynthIDDir             string
	MarkLLMScheme          string
	MarkLLMDir             string
	MarkLLMModel           string
	MarkLLMTimeout         int
	UpstreamScriptsDir     string
	CtrlRegenDir           string
	CtrlRegenIntensity     float64
	CtrlRegenSteps         int
	CtrlRegenDevice        string
	CtrlRegenSeed          int
	CtrlRegenSeedSet       bool
	CtrlRegenTimeout       int
	MarkDiffusionDir       string
	MarkDiffusionIntensity float64
	MarkDiffusionModel     string
	MarkDiffusionSize      int
	MarkDiffusionSteps     int
	MarkDiffusionDevice    string
	MarkDiffusionTimeout   int
	VideoVoteThreshold     float64
	VideoFrameFraction     float64
	VideoFrameFractionSet  bool
	ForceType              string
	ForceText              bool
	DeepImages             DeepImages
	Quiet                  bool
	JSON                   bool
	OnlyChanged            bool
	InPlace                bool
	Output                 string
	OutputDir              string
	Reflink                ReflinkMode
	Jobs                   int
	SkipUnknown            bool
	AudioRemix             bool
	AudioTempo             float64
	AudioPitch             float64
	AudioBitrate           string
	AudioCodec             string
	FFmpegTimeoutSeconds   int
	// DisableExternalTools is used for remote, byte-only audits. It prevents
	// optional local binaries such as c2patool/exiftool from seeing downloaded
	// content; ordinary file and HTTP API calls leave it false.
	DisableExternalTools bool
}

func DefaultOptions() Options {
	return Options{
		NormalizeSpaces:        true,
		DeepImages:             DeepAuto,
		Reflink:                ReflinkAuto,
		Jobs:                   1,
		SkipUnknown:            false,
		AudioTempo:             1.08,
		AudioPitch:             2,
		AudioBitrate:           "96k",
		CtrlRegenIntensity:     0.25,
		CtrlRegenSteps:         50,
		CtrlRegenTimeout:       3600,
		MarkDiffusionIntensity: 0.3,
		MarkDiffusionSize:      512,
		MarkDiffusionSteps:     50,
		MarkDiffusionTimeout:   3600,
		VideoVoteThreshold:     0.5,
		FFmpegTimeoutSeconds:   1800,
		Threshold:              0.65,
		RewriteTemperature:     0.9,
		RewriteTimeoutSeconds:  120,
		RewriteNoopLexFloor:    0.05,
		LayerAAfter:            false,
		AlsoLayerAText:         true,
	}
}

func stripAllMetadata(opts Options) bool {
	if opts.StripAllMetadataSet {
		return opts.StripAllMetadata
	}
	return !opts.KeepNonAIMetadata
}

type TextHit struct {
	Codepoint    string `json:"codepoint"`
	Label        string `json:"label"`
	Count        int    `json:"count"`
	Kind         string `json:"kind"`
	Confidence   string `json:"confidence"`
	SampleOffset []int  `json:"sample_offsets"`
}

type TextReport struct {
	Length          int            `json:"length"`
	SuspiciousTotal int            `json:"suspicious_total"`
	Hits            []TextHit      `json:"hits"`
	Notes           []string       `json:"notes"`
	Stylometry      map[string]any `json:"stylometry,omitempty"`
}

type FileReport struct {
	Path               string         `json:"path"`
	Kind               Kind           `json:"kind"`
	Format             string         `json:"format,omitempty"`
	HasC2PA            bool           `json:"has_c2pa,omitempty"`
	HasAIMetadata      bool           `json:"has_ai_metadata,omitempty"`
	SuspiciousTotal    int            `json:"suspicious_total,omitempty"`
	Findings           []string       `json:"findings,omitempty"`
	FindingsConfidence []string       `json:"findings_confidence,omitempty"`
	Details            map[string]any `json:"details,omitempty"`
	Tools              map[string]any `json:"tools,omitempty"`
	Notes              []string       `json:"notes,omitempty"`
	LayerAHits         []TextHit      `json:"layer_a_hits,omitempty"`
	Text               *TextReport    `json:"text,omitempty"`
	SynthID            map[string]any `json:"synthid,omitempty"`
}

// MarshalJSON keeps the public report shape compatible with the upstream
// inspectors. Internally text reports are attached as Text so callers can use
// one typed value for every pipeline; the Python reports flatten text fields
// and always expose the stable image/container fields instead.
func (r FileReport) MarshalJSON() ([]byte, error) {
	if r.Kind == KindUnknown {
		note := "unrecognized format; pass --as text|image|container|av or --force-text to override"
		if len(r.Notes) > 0 && r.Notes[0] != "" {
			note = r.Notes[0]
		}
		return json.Marshal(map[string]any{"kind": string(KindUnknown), "path": r.Path, "note": note})
	}
	if r.Kind == KindText && r.Text != nil {
		result := map[string]any{
			"kind":             string(KindText),
			"path":             r.Path,
			"length":           r.Text.Length,
			"suspicious_total": r.Text.SuspiciousTotal,
			"hits":             nonNilTextHits(r.Text.Hits),
			"notes":            nonNilStrings(r.Text.Notes),
		}
		if r.Text.Stylometry != nil {
			result["stylometry"] = r.Text.Stylometry
		}
		return json.Marshal(result)
	}

	result := map[string]any{
		"kind":                string(r.Kind),
		"path":                r.Path,
		"format":              r.Format,
		"has_c2pa":            r.HasC2PA,
		"has_ai_metadata":     r.HasAIMetadata,
		"findings":            nonNilStrings(r.Findings),
		"findings_confidence": nonNilStrings(r.FindingsConfidence),
		"notes":               nonNilStrings(r.Notes),
	}
	switch r.Kind {
	case KindImage:
		result["tools"] = nonNilMap(r.Tools)
		result["synthid"] = r.SynthID
	case KindContainer:
		result["tools"] = nonNilMap(r.Tools)
		result["details"] = nonNilMap(r.Details)
		result["suspicious_total"] = r.SuspiciousTotal
		result["layer_a_hits"] = nonNilTextHits(r.LayerAHits)
	case KindAV:
		// AVInspectReport has no tools/details or Layer-A fields.
	default:
		return nil, fmt.Errorf("cannot marshal report kind %q", r.Kind)
	}
	return json.Marshal(result)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilTextHits(values []TextHit) []TextHit {
	if values == nil {
		return []TextHit{}
	}
	return values
}

func nonNilMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}

type CleanResult struct {
	Kind          Kind           `json:"kind"`
	Format        string         `json:"format,omitempty"`
	Input         string         `json:"input,omitempty"`
	Output        string         `json:"output,omitempty"`
	Changed       bool           `json:"changed"`
	BytesIn       int64          `json:"bytes_in"`
	BytesOut      int64          `json:"bytes_out"`
	Stats         map[string]any `json:"stats,omitempty"`
	Actions       []string       `json:"actions,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
	StillHasC2PA  bool           `json:"still_has_c2pa,omitempty"`
	StillHasAI    bool           `json:"still_has_ai_metadata,omitempty"`
	PostFindings  []string       `json:"post_findings,omitempty"`
	Partial       bool           `json:"partial,omitempty"`
	PixelRemoval  map[string]any `json:"pixel_removal,omitempty"`
	SynthIDBefore map[string]any `json:"synthid_before,omitempty"`
	SynthIDAfter  map[string]any `json:"synthid_after,omitempty"`
	// AudioMarkRemoval is populated by the HTTP service's optional audio
	// chain.  It is kept on the clean result so the service can match the
	// upstream report shape without hiding an unavailable backend.
	AudioMarkRemoval map[string]any `json:"audio_mark_removal,omitempty"`
}

// MarshalJSON preserves the upstream clean-report contract for fields whose
// zero values are meaningful. The internal Go result keeps optional fields
// sparse, but the Python cleaners always emit residual verdicts/findings and
// the image cleaner always emits its optional scorer/backend slots.
func (r CleanResult) MarshalJSON() ([]byte, error) {
	type plain CleanResult
	data, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	switch r.Kind {
	case KindImage:
		report["still_has_c2pa"] = r.StillHasC2PA
		report["still_has_ai_metadata"] = r.StillHasAI
		postFindings := r.PostFindings
		if postFindings == nil {
			postFindings = []string{}
		}
		report["post_findings"] = postFindings
		report["synthid_before"] = r.SynthIDBefore
		report["synthid_after"] = r.SynthIDAfter
		report["pixel_removal"] = r.PixelRemoval
	case KindAV, KindContainer:
		report["still_has_c2pa"] = r.StillHasC2PA
		report["still_has_ai_metadata"] = r.StillHasAI
		postFindings := r.PostFindings
		if postFindings == nil {
			postFindings = []string{}
		}
		report["post_findings"] = postFindings
		if r.Kind == KindContainer {
			report["meta"] = map[string]any{"format": r.Format}
		}
	}
	return json.Marshal(report)
}

type BatchItem struct {
	Path   string       `json:"path"`
	Output string       `json:"output,omitempty"`
	Result *CleanResult `json:"result,omitempty"`
	Report *FileReport  `json:"report,omitempty"`
	Error  string       `json:"error,omitempty"`
}

type BatchSummary struct {
	Input   string      `json:"input"`
	Output  string      `json:"output"`
	Files   int         `json:"files"`
	Changed int         `json:"changed"`
	Errors  int         `json:"errors"`
	Items   []BatchItem `json:"items"`
}
