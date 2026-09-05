package core

import (
	"regexp"
	"strings"
)

var humanizePhraseSwaps = []struct {
	pattern *regexp.Regexp
	repl    string
}{
	{regexp.MustCompile("(?i)\\bin order to\\b"), "to"},
	{regexp.MustCompile("(?i)\\bdue to the fact that\\b"), "because"},
	{regexp.MustCompile("(?i)\\bat this point in time\\b"), "now"},
	{regexp.MustCompile("(?i)\\bin the event that\\b"), "if"},
	{regexp.MustCompile("(?i)\\bhas the ability to\\b"), "can"},
	{regexp.MustCompile("(?i)\\bhave the ability to\\b"), "can"},
	{regexp.MustCompile("(?i)\\bit is important to note that\\b"), "note that"},
	{regexp.MustCompile("(?i)\\bit is worth noting that\\b"), "note that"},
	{regexp.MustCompile("(?i)\\bdelve into\\b"), "explore"},
	{regexp.MustCompile("(?i)\\bdelves into\\b"), "explores"},
	{regexp.MustCompile("(?i)\\bdelved into\\b"), "explored"},
	{regexp.MustCompile("(?i)\\bdelving into\\b"), "exploring"},
}

var humanizeWordRE = regexp.MustCompile("(?i)\\b(utilize|utilizes|utilized|utilizing)\\b")
var humanizeDashRE = regexp.MustCompile("\\s*[—–]\\s*")
var humanizeDoubleHyphenRE = regexp.MustCompile("(\\s*)--(\\s*)")
var humanizeCommaRE = regexp.MustCompile(",\\s*,\\s*")
var humanizeCommaPunctuationRE = regexp.MustCompile(",\\s*([.!?])")

func capitalizeLike(matched, replacement string) string {
	if matched == strings.ToUpper(matched) {
		return strings.ToUpper(replacement)
	}
	if matched != "" && matched[0] >= 'A' && matched[0] <= 'Z' {
		return strings.ToUpper(replacement[:1]) + replacement[1:]
	}
	return replacement
}

func replaceHumanizeDashes(text string) string {
	replace := func(input string, re *regexp.Regexp, doubleHyphen bool) string {
		matches := re.FindAllStringIndex(input, -1)
		if len(matches) == 0 {
			return input
		}
		var builder strings.Builder
		last := 0
		for _, span := range matches {
			builder.WriteString(input[last:span[0]])
			left, right := "", ""
			if span[0] > 0 {
				left = input[span[0]-1 : span[0]]
			}
			if span[1] < len(input) {
				right = input[span[1] : span[1]+1]
			}
			matched := input[span[0]:span[1]]
			leftAlphaNumeric := (left >= "a" && left <= "z") || (left >= "A" && left <= "Z") || (left >= "0" && left <= "9")
			rightAlphaNumeric := (right >= "a" && right <= "z") || (right >= "A" && right <= "Z") || (right >= "0" && right <= "9")
			numericRange := !doubleHyphen && left >= "0" && left <= "9" && right >= "0" && right <= "9"
			commandOption := doubleHyphen && rightAlphaNumeric && !leftAlphaNumeric
			if numericRange || commandOption {
				builder.WriteString(matched)
			} else {
				builder.WriteString(", ")
			}
			last = span[1]
		}
		builder.WriteString(input[last:])
		return builder.String()
	}
	text = replace(text, humanizeDashRE, false)
	text = replace(text, humanizeDoubleHyphenRE, true)
	text = humanizeCommaRE.ReplaceAllString(text, ", ")
	return humanizeCommaPunctuationRE.ReplaceAllString(text, "$1")
}

// HumanizeText applies the context-free part of the upstream humanize tactic.
// It deliberately leaves judgment-dependent edits to the rewrite prompt.
func HumanizeText(text string) string {
	text = strings.NewReplacer(
		"‘", "'", "’", "'", "“", "\"", "”", "\"",
	).Replace(text)
	text = replaceHumanizeDashes(text)
	for _, swap := range humanizePhraseSwaps {
		text = swap.pattern.ReplaceAllStringFunc(text, func(match string) string {
			return capitalizeLike(match, swap.repl)
		})
	}
	text = humanizeWordRE.ReplaceAllStringFunc(text, func(match string) string {
		replacements := map[string]string{"utilize": "use", "utilizes": "uses", "utilized": "used", "utilizing": "using"}
		return capitalizeLike(match, replacements[strings.ToLower(match)])
	})
	return text
}
