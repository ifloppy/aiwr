package core

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	minStylometryWords = 30
	fullWeightWords    = 100
)

type styleMarker struct {
	pattern *regexp.Regexp
	label   string
	weight  float64
}

var styleMarkers = []styleMarker{
	{regexp.MustCompile(`(?i)\bdelve(?:s|d)?\s+into\b`), "delve into", 1.2},
	{regexp.MustCompile(`(?i)\ba\s+testament\s+to\b`), "a testament to", 1.1},
	{regexp.MustCompile(`(?i)\brich\s+tapestry(?:\s+of)?\b`), "rich tapestry", 1.3},
	{regexp.MustCompile(`(?i)\bplays?\s+a\s+(?:pivotal|crucial|vital|key)\s+role\b`), "plays a pivotal/crucial role", 1.0},
	{regexp.MustCompile(`(?i)\bin\s+(?:today'?s|the)\s+(?:(?:fast-paced|ever-evolving|digital|rapidly\s+changing)\s+)*(?:world|landscape|era|environment)\b`), "in today's fast-paced world/landscape", 1.4},
	{regexp.MustCompile(`(?i)\bit\s+is\s+(?:important|essential|crucial|worth\s+noting)\s+to\s+(?:note|remember|consider|highlight)\b`), "it is important/crucial to note", 0.9},
	{regexp.MustCompile(`(?i)\bnot\s+only\b[\w\s,]+\bbut\s+(?:also\s+)?(?:serves\s+to|acts\s+as|highlights)\b`), "not only ... but also serves to", 0.8},
	{regexp.MustCompile(`(?i)\bserve(?:s|d)?\s+as\s+a\s+(?:beacon|reminder|catalyst|cornerstone)\b`), "serves as a beacon/catalyst/cornerstone", 1.1},
	{regexp.MustCompile(`(?i)\bunderscore(?:s|d)?\s+the\s+(?:importance|need|significance)\b`), "underscores the importance/need", 0.9},
	{regexp.MustCompile(`(?i)\bfoster(?:s|ing|ed)?\s+a\s+(?:sense|culture|deeper\s+understanding)\b`), "fosters a sense/culture", 0.9},
	{regexp.MustCompile(`(?i)\bseamlessly\s+(?:integrates?|integrated|blends?|combine[sd]?)\b`), "seamlessly integrates/blends", 1.0},
	{regexp.MustCompile(`(?i)\bnavigat(?:e|ing|es|ed)\s+the\s+(?:complexities|intricacies|nuances)\b`), "navigating the complexities/nuances", 1.0},
	{regexp.MustCompile(`(?i)\bmultifaceted\s+(?:nature|approach|landscape)\b`), "multifaceted nature/approach", 1.0},
	{regexp.MustCompile(`(?i)\bharness(?:ing|ed|es)?\s+the\s+power\s+of\b`), "harnessing the power of", 1.0},
	{regexp.MustCompile(`(?i)\ba\s+myriad\s+of\b`), "a myriad of", 0.8},
	{regexp.MustCompile(`(?i)\bparadigm\s+shift\b`), "paradigm shift", 0.9},
	{regexp.MustCompile(`(?i)\bholistic\s+(?:approach|view|perspective)\b`), "holistic approach/perspective", 0.9},
	{regexp.MustCompile(`(?i)\bin\s+conclusion\b[,\s]`), "in conclusion", 0.8},
	{regexp.MustCompile(`(?i)\bto\s+summarize\b[,\s]`), "to summarize", 0.8},
	{regexp.MustCompile(`(?i)\bultimately\b[,\s]`), "ultimately,", 0.6},
	{regexp.MustCompile(`(?i)\bfurthermore\b[,\s]`), "furthermore,", 0.6},
	{regexp.MustCompile(`(?i)\bmoreover\b[,\s]`), "moreover,", 0.6},
	{regexp.MustCompile(`(?i)\bas\s+an\s+ai\b`), "as an AI", 1.5},
	{regexp.MustCompile(`(?i)\bi\s+hope\s+this\s+helps\b`), "I hope this helps", 1.2},
	{regexp.MustCompile(`(?i)\bstands?\s+as\s+a\s+testament\b`), "stands as a testament to", 1.1},
	{regexp.MustCompile(`(?i)\bmark(?:s|ing)?\s+an?\s+(?:indelible|pivotal|significant|new)\s+(?:moment|chapter|milestone)\b`), "marking a pivotal moment/chapter", 1.0},
	{regexp.MustCompile(`(?i)\b(?:reflecting|symbolizing|showcasing|underscoring)\s+(?:the|a|its)\b`), "shallow -ing analysis (reflecting/symbolizing/showcasing)", 0.9},
	{regexp.MustCompile(`(?i)\b(?:nestled|vibrant|breathtaking)\b`), "sales language (nestled/vibrant)", 0.8},
	{regexp.MustCompile(`(?i)\b(?:game[- ]changer|game-changing)\b`), "game-changer", 0.9},
	{regexp.MustCompile(`(?i)\b(?:unparalleled|unprecedented)\b`), "unparalleled/unprecedented", 0.7},
	{regexp.MustCompile(`(?i)\b(?:world-class|state-of-the-art|cutting-edge)\b`), "world-class/state-of-the-art", 0.8},
	{regexp.MustCompile(`(?i)\b(?:revolutionary|groundbreaking)\b`), "revolutionary/groundbreaking", 0.6},
	{regexp.MustCompile(`(?i)\b(?:leverag(?:e|ing|ed|es)|utiliz(?:e|ing|ed|es))\b`), "leverage/utilize", 0.8},
	{regexp.MustCompile(`(?i)\bboasts?\b`), "boasts (copula avoidance)", 0.6},
	{regexp.MustCompile(`(?i)\bit['’]?s\s+not\s+just\b`), "it's not just X, it's Y", 0.8},
	{regexp.MustCompile(`(?i)\b(?:not\s+just\b.{0,60}\bbut\s+also\b)`), "not just X but also Y", 0.8},
	{regexp.MustCompile(`(?i)\bdive(?:s|d)?\s+into\b`), "dive into", 0.7},
	{regexp.MustCompile(`(?i)\blet['’]?s\s+(?:dive\s+in|get\s+started)\b`), "let's dive in", 0.7},
	{regexp.MustCompile(`(?i)\bin\s+order\s+to\b`), "in order to", 0.6},
	{regexp.MustCompile(`(?i)\bdue\s+to\s+the\s+fact\s+that\b`), "due to the fact that", 0.8},
	{regexp.MustCompile(`(?i)\bit(?:['’]s| is)\s+worth\s+noting\s+that\b`), "it is worth noting that", 0.8},
	{regexp.MustCompile(`(?i)\b(?:needless\s+to\s+say|it\s+goes\s+without\s+saying)\b`), "needless to say", 0.8},
	{regexp.MustCompile(`(?i)\bthe\s+future\s+looks\s+bright\b`), "the future looks bright", 0.9},
	{regexp.MustCompile(`(?i)\b(?:sure\s+thing!?|great\s+question!?|happy\s+to\s+help)\b`), "assistant chatter (sure thing/great question)", 0.6},
}

func styleWords(text string) []string {
	// Python's upstream expression is ``\b[\w'-]+\b`` with Unicode
	// semantics. Go's regexp \b is ASCII-only, so scan the same character
	// class explicitly: Unicode letters/numbers and underscore are word
	// characters; apostrophe and hyphen may occur inside a token but cannot
	// satisfy either boundary. Curly apostrophes are deliberately excluded,
	// matching the upstream expression (the phrase patterns handle them where
	// they are meaningful).
	words := []string{}
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		start := i
		if !styleWordRune(text[i : i+size]) {
			i += size
			continue
		}
		for i += size; i < len(text); {
			_, nextSize := utf8.DecodeRuneInString(text[i:])
			if nextSize == 0 || !styleWordClassRune(text[i:i+nextSize]) {
				break
			}
			i += nextSize
		}
		end := i
		for start < end {
			_, n := utf8.DecodeRuneInString(text[start:end])
			if styleWordRune(text[start : start+n]) {
				break
			}
			start += n
		}
		for end > start {
			_, n := utf8.DecodeLastRuneInString(text[start:end])
			if styleWordRune(text[end-n : end]) {
				break
			}
			end -= n
		}
		if start < end {
			words = append(words, strings.ToLower(text[start:end]))
		}
	}
	return words
}

func styleWordRune(value string) bool {
	r, _ := utf8.DecodeRuneInString(value)
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

func styleWordClassRune(value string) bool {
	r, _ := utf8.DecodeRuneInString(value)
	return styleWordRune(value) || r == '\'' || r == '-'
}

func styleSentences(text string) []string {
	lines := []string{}
	inCode := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		if !inCode && trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	clean := strings.Join(lines, "\n")
	if clean == "" {
		return nil
	}
	parts := regexp.MustCompile(`[.!?]+\s+|\n+`).Split(clean, -1)
	result := []string{}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			result = append(result, strings.TrimSpace(part))
		}
	}
	return result
}

func styleMATTR(words []string, window int) float64 {
	if len(words) == 0 {
		return 0
	}
	if len(words) <= window {
		return float64(uniqueWords(words)) / float64(len(words))
	}
	total := 0.0
	count := 0
	for start := 0; start+window <= len(words); start++ {
		total += float64(uniqueWords(words[start:start+window])) / float64(window)
		count++
	}
	return total / float64(count)
}

func uniqueWords(words []string) int {
	seen := map[string]bool{}
	for _, word := range words {
		seen[word] = true
	}
	return len(seen)
}

func styleMarkerMatches(text string) []map[string]any {
	result := []map[string]any{}
	for _, marker := range styleMarkers {
		matches := marker.pattern.FindAllString(text, -1)
		if len(matches) == 0 {
			continue
		}
		spans := marker.pattern.FindAllStringIndex(text, 10)
		serialized := make([][]int, len(spans))
		for i, span := range spans {
			serialized[i] = []int{span[0], span[1]}
		}
		result = append(result, map[string]any{
			"phrase": marker.label, "count": len(matches), "weight": marker.weight,
			"samples": matches[:minInt(3, len(matches))], "spans": serialized,
		})
	}
	return result
}

func styleBurstiness(sentences []string) (float64, bool) {
	lengths := []float64{}
	for _, sentence := range sentences {
		n := len(styleWords(sentence))
		if n > 0 {
			lengths = append(lengths, float64(n))
		}
	}
	if len(lengths) < 2 {
		return 0, false
	}
	mean := 0.0
	for _, n := range lengths {
		mean += n
	}
	mean /= float64(len(lengths))
	variance := 0.0
	for _, n := range lengths {
		variance += (n - mean) * (n - mean)
	}
	variance /= float64(len(lengths) - 1)
	if mean == 0 {
		return 0, false
	}
	return math.Sqrt(variance) / mean, true
}

func scoreStylometry(text, path string, _ float64) map[string]any {
	words := styleWords(text)
	sentences := styleSentences(text)
	markers := styleMarkerMatches(text)
	findings := []string{}
	notes := []string{}
	mattr := styleMATTR(words, 50)
	base := map[string]any{
		"path": path, "word_count": len(words), "sentence_count": len(sentences),
		"burstiness_cv": nil, "lexical_diversity": roundStyleMetric(mattr), "ai_ngram_density": 0.0,
		"matched_markers": markers, "score": nil, "confidence_level": nil,
		"density_tier": "uncalibrated", "status": "insufficient_length", "findings": findings,
		"findings_confidence": make([]string, len(findings)), "notes": notes,
	}
	if len(words) < minStylometryWords {
		findings = findings[:0]
		for _, marker := range markers {
			findings = append(findings, fmt.Sprintf("AI phrase marker '%s' found (%dx)", marker["phrase"], marker["count"]))
		}
		base["findings"] = findings
		confidences := make([]string, len(findings))
		for i := range confidences {
			confidences[i] = findingConfidence(findings[i])
		}
		base["findings_confidence"] = confidences
		notes = append(notes, fmt.Sprintf("Sample contains %d words; statistical stylometry is uncalibrated below 30 words, so no score is reported", len(words)))
		base["notes"] = notes
		return base
	}
	cv, hasCV := styleBurstiness(sentences)
	base["burstiness_cv"] = cv
	if !hasCV {
		base["burstiness_cv"] = nil
		notes = append(notes, "Sentence burstiness unavailable (fewer than 2 parsed sentences — e.g. body wrapped in a code fence); composite renormalized over AI-phrase density and lexical diversity")
	}
	weight := 0.0
	for _, marker := range markers {
		weight += float64(marker["count"].(int)) * marker["weight"].(float64)
	}
	density := weight / (float64(len(words)) / 100)
	base["ai_ngram_density"] = density
	burstScore := 0.0
	if hasCV {
		switch {
		case cv < 0.25:
			burstScore = 0.95
		case cv < 0.35:
			burstScore = 0.80
		case cv < 0.45:
			burstScore = 0.50
		case cv < 0.55:
			burstScore = 0.25
		default:
			burstScore = 0.05
		}
	}
	densityScore := 0.0
	switch {
	case density >= 2:
		densityScore = 1
	case density >= 1:
		densityScore = 0.75
	case density >= 0.5:
		densityScore = 0.45
	case density > 0:
		densityScore = 0.20
	}
	diversityScore := 0.1
	if mattr >= 0.68 && mattr <= 0.76 {
		diversityScore = 0.4
	}
	composite := 0.0
	if hasCV {
		composite = burstScore*0.45 + densityScore*0.45 + diversityScore*0.10
	} else {
		composite = (densityScore*0.45 + diversityScore*0.10) / 0.55
	}
	dampener := 1.0
	if len(words) < fullWeightWords {
		dampener = 0.4 + 0.6*float64(len(words)-minStylometryWords)/float64(fullWeightWords-minStylometryWords)
		notes = append(notes, fmt.Sprintf("Sample word count (%d) is in calibration range (30-100); score dampened by factor %.2f", len(words), dampener))
	}
	score := math.Max(0, math.Min(1, composite*dampener))
	base["score"] = roundStyleMetric(score)
	base["status"] = "ok"
	base["density_tier"] = "low"
	if score >= 0.65 {
		base["density_tier"] = "high"
	} else if score >= 0.4 {
		base["density_tier"] = "medium"
	}
	confidence := "CLEAN"
	if score >= 0.75 {
		confidence = "HIGH"
	} else if score >= 0.50 {
		confidence = "MEDIUM"
	} else if score >= 0.25 {
		confidence = "LOW"
	}
	base["confidence_level"] = confidence
	for _, marker := range markers {
		findings = append(findings, fmt.Sprintf("AI cadence phrase '%s' (%dx)", marker["phrase"], marker["count"]))
	}
	if hasCV && cv < 0.35 && len(sentences) >= 3 {
		findings = append(findings, fmt.Sprintf("Unnaturally uniform sentence cadence (CV=%.2f < 0.35)", cv))
	}
	if density >= 1 {
		findings = append(findings, fmt.Sprintf("Elevated AI formulaic transition density (%.2f/100w)", density))
	}
	base["findings"] = findings
	confidences := make([]string, len(findings))
	for i := range confidences {
		confidences[i] = findingConfidence(findings[i])
	}
	base["findings_confidence"] = confidences
	base["burstiness_cv"] = roundStyleMetricOrNil(base["burstiness_cv"])
	base["lexical_diversity"] = roundStyleMetric(base["lexical_diversity"].(float64))
	base["ai_ngram_density"] = roundStyleMetric(base["ai_ngram_density"].(float64))
	base["notes"] = notes
	return base
}

func roundStyleMetric(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func roundStyleMetricOrNil(value any) any {
	if number, ok := value.(float64); ok {
		return roundStyleMetric(number)
	}
	return nil
}

func stylometrySuspicious(report map[string]any) bool {
	return stylometrySuspiciousAt(report, 0.65)
}

func stylometrySuspiciousAt(report map[string]any, threshold float64) bool {
	if report == nil || report["status"] != "ok" {
		return false
	}
	score, ok := report["score"].(float64)
	if threshold <= 0 {
		threshold = 0.65
	}
	return ok && score >= threshold
}

// StylometrySuspicious reports whether a score map produced by this package
// crosses its own threshold. A short document or an uncalibrated report is
// deliberately never treated as suspicious.
func StylometrySuspicious(report map[string]any) bool {
	return stylometrySuspicious(report)
}

// StylometrySuspiciousAt applies a caller-selected exit/verdict threshold
// without adding that implementation detail to the upstream-compatible JSON
// report.
func StylometrySuspiciousAt(report map[string]any, threshold float64) bool {
	return stylometrySuspiciousAt(report, threshold)
}

func sortedStyleKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
