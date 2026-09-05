package core

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var stripCodepoints = func() map[rune]bool {
	m := make(map[rune]bool)
	for _, r := range []rune{
		0x00ad, 0x034f, 0x061c, 0x115f, 0x1160, 0x17b4, 0x17b5,
		0x180b, 0x180c, 0x180d, 0x180e, 0x180f, 0x200b, 0x200c,
		0x200d, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d,
		0x202e, 0x2060, 0x2061, 0x2062, 0x2063, 0x2064, 0x2066,
		0x2067, 0x2068, 0x2069, 0x206a, 0x206b, 0x206c, 0x206d,
		0x206e, 0x206f, 0xfeff, 0x3164, 0xffa0, 0xfff9, 0xfffa,
		0xfffb,
	} {
		m[r] = true
	}
	for r := rune(0xfe00); r <= 0xfe0f; r++ {
		m[r] = true
	}
	return m
}()

var spaceHomoglyphs = map[rune]rune{
	0x00a0: ' ', 0x1680: ' ', 0x2000: ' ', 0x2001: ' ', 0x2002: ' ',
	0x2003: ' ', 0x2004: ' ', 0x2005: ' ', 0x2006: ' ', 0x2007: ' ',
	0x2008: ' ', 0x2009: ' ', 0x200a: ' ', 0x202f: ' ', 0x205f: ' ',
	0x3000: ' ',
}

var latinConfusables = func() map[rune]rune {
	m := map[rune]rune{
		0x0410: 'A', 0x0412: 'B', 0x0415: 'E', 0x041a: 'K', 0x041c: 'M', 0x041d: 'H',
		0x041e: 'O', 0x0420: 'P', 0x0421: 'C', 0x0422: 'T', 0x0425: 'X', 0x0430: 'a',
		0x0435: 'e', 0x043e: 'o', 0x0440: 'p', 0x0441: 'c', 0x0443: 'y', 0x0445: 'x',
		0x0456: 'i',
	}
	for r := rune(0xff21); r <= 0xff3a; r++ {
		m[r] = 'A' + (r - 0xff21)
	}
	for r := rune(0xff41); r <= 0xff5a; r++ {
		m[r] = 'a' + (r - 0xff41)
	}
	return m
}()

var bidiCodepoints = map[rune]bool{
	0x061c: true, 0x200e: true, 0x200f: true, 0x202a: true, 0x202b: true,
	0x202c: true, 0x202d: true, 0x202e: true, 0x2066: true, 0x2067: true,
	0x2068: true, 0x2069: true,
}

var preservableBidi = map[rune]bool{
	0x061c: true, 0x200e: true, 0x200f: true, 0x2066: true, 0x2067: true,
	0x2068: true, 0x2069: true,
}

var orthographicCF = map[rune]bool{
	0x0600: true, 0x0601: true, 0x0602: true, 0x0603: true, 0x0604: true,
	0x0605: true, 0x06dd: true, 0x070f: true, 0x08e2: true, 0x110bd: true,
	0x110cd: true,
}

var scriptJoiners = map[rune]bool{0x200c: true, 0x200d: true}
var mongolianFVS = map[rune]bool{0x180b: true, 0x180c: true, 0x180d: true, 0x180f: true}
var khmerVowels = map[rune]bool{0x17b4: true, 0x17b5: true}
var hangulFillers = map[rune]bool{0x115f: true, 0x1160: true, 0x3164: true, 0xffa0: true}

type textUnit struct {
	r     rune
	raw   []byte
	valid bool
}

func decodeTextUnits(data []byte) []textUnit {
	units := make([]textUnit, 0, utf8.RuneCount(data))
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			// Python's UTF-8 surrogateescape represents each undecodable byte
			// as U+DCxx. Keep the original byte for output, but retain the
			// surrogate code point for the context-sensitive glue rules.
			units = append(units, textUnit{r: 0xdc00 + rune(data[i]), raw: append([]byte(nil), data[i]), valid: false})
			i++
			continue
		}
		units = append(units, textUnit{r: r, raw: data[i : i+size], valid: true})
		i += size
	}
	return units
}

func isCJK(r rune) bool {
	return r >= 0x3400 && r <= 0x4dbf || r >= 0x4e00 && r <= 0x9fff || r >= 0xf900 && r <= 0xfaff || r >= 0x20000 && r <= 0x323af
}

func isMongolianBase(r rune) bool   { return r >= 0x1800 && r <= 0x18af }
func isMongolianLetter(r rune) bool { return isMongolianBase(r) && unicode.IsLetter(r) }
func isKhmerLetter(r rune) bool     { return r >= 0x1780 && r <= 0x17ff && unicode.IsLetter(r) }
func isHangulJamo(r rune) bool {
	return r >= 0x1100 && r <= 0x11ff || r >= 0xa960 && r <= 0xa97c || r >= 0xd7b0 && r <= 0xd7c6 || r >= 0x3131 && r <= 0x318e || r >= 0xffa1 && r <= 0xffdc
}

func joiningScript(r rune) string {
	groups := []struct {
		lo, hi rune
		name   string
	}{
		{0x0600, 0x08ff, "arabic"}, {0x0900, 0x0dff, "indic"}, {0x0f00, 0x109f, "south-asian"},
		{0x1780, 0x17ff, "khmer"}, {0x1800, 0x18af, "mongolian"},
	}
	for _, g := range groups {
		if r >= g.lo && r <= g.hi && (unicode.IsLetter(r) || unicode.IsMark(r)) {
			return g.name
		}
	}
	return ""
}

func isEmojiBase(r rune) bool {
	if r >= 0x1f000 && r <= 0x1faff || r >= 0x2190 && r <= 0x25ff || r >= 0x2600 && r <= 0x27bf || r >= 0x2b00 && r <= 0x2bff {
		return true
	}
	if strings.ContainsRune("‼⁉ℹ⤴⤵©®™〰〽㊗㊙", r) {
		return true
	}
	return r == '#' || r == '*' || r >= '0' && r <= '9'
}

func isEmojiGlue(r rune) bool { return r == 0x200d || r == 0xfe0e || r == 0xfe0f }
func isVariationSelector(r rune) bool {
	return r >= 0xe0100 && r <= 0xe01ef || r >= 0xfe00 && r <= 0xfe0f || mongolianFVS[r]
}
func isPrivateUse(r rune) bool {
	return r >= 0xe000 && r <= 0xf8ff || r >= 0xf0000 && r <= 0xffffd || r >= 0x100000 && r <= 0x10fffd
}
func isNoncharacter(r rune) bool { return r >= 0xfdd0 && r <= 0xfdef || r&0xfffe == 0xfffe }
func isReservedIgnorable(r rune) bool {
	if r == 0x2065 || r == 0xe0000 {
		return true
	}
	return r >= 0xfff0 && r <= 0xfff8 || r >= 0xe0080 && r <= 0xe00ff || r >= 0xe01f0 && r <= 0xe0fff
}
func isLayoutCF(r rune) bool {
	return r >= 0x13430 && r <= 0x1343f || r >= 0x1bca0 && r <= 0x1bca3 || r >= 0x1d173 && r <= 0x1d17a
}
func layoutScript(r rune) (rune, rune, bool) {
	switch {
	case r >= 0x13430 && r <= 0x1343f:
		return 0x13000, 0x14400, true
	case r >= 0x1bca0 && r <= 0x1bca3:
		return 0x1bc00, 0x1bca4, true
	case r >= 0x1d173 && r <= 0x1d17a:
		return 0x1d100, 0x1d200, true
	default:
		return 0, 0, false
	}
}
func isTag(r rune) bool    { return r >= 0xe0001 && r <= 0xe007f }
func isTagRun(r rune) bool { return r >= 0xe0020 && r <= 0xe007f }
func isGlue(r rune) bool {
	return isEmojiGlue(r) || isVariationSelector(r) || scriptJoiners[r] || isTagRun(r) || mongolianFVS[r] || khmerVowels[r] || hangulFillers[r]
}

func validFlagTags(units []textUnit) map[int]bool {
	valid := map[int]bool{}
	for i := 0; i < len(units); i++ {
		if !units[i].valid || units[i].r != 0x1f3f4 {
			continue
		}
		j := i + 1
		for j < len(units) && units[j].valid && units[j].r >= 0xe0020 && units[j].r <= 0xe007e {
			j++
		}
		if j > i+1 && j < len(units) && units[j].valid && units[j].r == 0xe007f {
			for k := i + 1; k <= j; k++ {
				valid[k] = true
			}
			i = j
		}
	}
	return valid
}

func validBidiEmbeddings(units []textUnit) map[int]bool {
	valid := map[int]bool{}
	type opener struct {
		r     rune
		index int
	}
	stack := []opener{}
	for i, u := range units {
		if !u.valid {
			continue
		}
		switch u.r {
		case 0x202a, 0x202b, 0x202d, 0x202e:
			stack = append(stack, opener{u.r, i})
		case 0x202c:
			if len(stack) == 0 {
				continue
			}
			op := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if op.r == 0x202a || op.r == 0x202b {
				valid[op.index] = true
				valid[i] = true
			}
		}
	}
	return valid
}

func isStripCP(r rune) bool {
	return stripCodepoints[r] || isVariationSelector(r) && r >= 0xe0100 && r <= 0xe01ef || isTag(r) || isNoncharacter(r) || isReservedIgnorable(r) || isPrivateUse(r)
}

func stripKind(r rune) string {
	switch {
	case isTag(r):
		return "tag_chars"
	case isNoncharacter(r):
		return "noncharacter"
	case isReservedIgnorable(r):
		return "reserved_ignorable"
	case isVariationSelector(r):
		return "variation_selector"
	case bidiCodepoints[r]:
		return "bidi"
	case r == 0x200b || r == 0x200c || r == 0x200d || r == 0x2060 || r == 0xfeff || r == 0x180e:
		return "zwj_family"
	case isPrivateUse(r):
		return "private_use"
	default:
		return "strip"
	}
}

func decide(u textUnit, prevKept *rune, prevInput *rune, nextInput *rune, flagTag, bidiEmbedding bool, normalizeSpaces, confusables, stripEmoji, stripBidi bool) (action, out string, kind string) {
	if !u.valid {
		return "keep", string(u.raw), ""
	}
	r := u.r
	if bidiEmbedding && !stripBidi || preservableBidi[r] && !stripBidi {
		return "keep", string(r), ""
	}
	if prevInput != nil && !stripEmoji {
		p := *prevInput
		switch {
		case r >= 0xe0100 && r <= 0xe01ef && isCJK(p):
			return "keep", string(r), ""
		case mongolianFVS[r] && isMongolianBase(p):
			return "keep", string(r), ""
		case r >= 0xfe00 && r <= 0xfe0d && isCJK(p):
			return "keep", string(r), ""
		}
	}
	if isEmojiGlue(r) && !stripEmoji {
		if (r == 0xfe0e || r == 0xfe0f) && prevInput != nil && isEmojiBase(*prevInput) {
			return "keep", string(r), ""
		}
		if r == 0x200d && prevKept != nil && nextInput != nil && isEmojiBase(*prevKept) && isEmojiBase(*nextInput) {
			return "keep", string(r), ""
		}
	}
	if !stripEmoji {
		if scriptJoiners[r] && prevInput != nil && nextInput != nil {
			ps, ns := joiningScript(*prevInput), joiningScript(*nextInput)
			if ps != "" && ps == ns {
				return "keep", string(r), ""
			}
		}
		if isTagRun(r) && flagTag {
			return "keep", string(r), ""
		}
		if mongolianFVS[r] && prevKept != nil && isMongolianLetter(*prevKept) {
			return "keep", string(r), ""
		}
		if khmerVowels[r] && prevKept != nil && isKhmerLetter(*prevKept) {
			return "keep", string(r), ""
		}
		if hangulFillers[r] && prevKept != nil && isHangulJamo(*prevKept) {
			return "keep", string(r), ""
		}
		if orthographicCF[r] {
			return "keep", string(r), ""
		}
		if lo, hi, ok := layoutScript(r); ok && ((prevInput != nil && *prevInput >= lo && *prevInput < hi) || (nextInput != nil && *nextInput >= lo && *nextInput < hi)) {
			return "keep", string(r), ""
		}
	}
	if isStripCP(r) {
		return "strip", "", stripKind(r)
	}
	if normalizeSpaces {
		if replacement, ok := spaceHomoglyphs[r]; ok {
			return "replace", string(replacement), "space"
		}
	}
	if confusables {
		if replacement, ok := latinConfusables[r]; ok {
			return "replace", string(replacement), "confusable"
		}
	}
	if unicode.In(r, unicode.Cf) {
		return "strip", "", "other_cf"
	}
	return "keep", string(r), ""
}

func runeLabel(r rune) string {
	name := map[rune]string{
		0x00ad: "SOFT HYPHEN", 0x034f: "COMBINING GRAPHEME JOINER", 0x061c: "ARABIC LETTER MARK",
		0x115f: "HANGUL CHOSEONG FILLER", 0x1160: "HANGUL JUNGSEONG FILLER",
		0x17b4: "KHMER VOWEL INHERENT AQ", 0x17b5: "KHMER VOWEL INHERENT AA",
		0x180b: "MONGOLIAN FREE VARIATION SELECTOR ONE", 0x180c: "MONGOLIAN FREE VARIATION SELECTOR TWO",
		0x180d: "MONGOLIAN FREE VARIATION SELECTOR THREE", 0x180e: "MONGOLIAN VOWEL SEPARATOR",
		0x180f: "MONGOLIAN FREE VARIATION SELECTOR FOUR", 0x200b: "ZERO WIDTH SPACE",
		0x200c: "ZERO WIDTH NON-JOINER", 0x200d: "ZERO WIDTH JOINER", 0x200e: "LEFT-TO-RIGHT MARK",
		0x200f: "RIGHT-TO-LEFT MARK", 0x202a: "LEFT-TO-RIGHT EMBEDDING", 0x202b: "RIGHT-TO-LEFT EMBEDDING",
		0x202c: "POP DIRECTIONAL FORMATTING", 0x202d: "LEFT-TO-RIGHT OVERRIDE", 0x202e: "RIGHT-TO-LEFT OVERRIDE",
		0x2060: "WORD JOINER", 0x2061: "FUNCTION APPLICATION", 0x2062: "INVISIBLE TIMES",
		0x2063: "INVISIBLE SEPARATOR", 0x2064: "INVISIBLE PLUS", 0x2066: "LEFT-TO-RIGHT ISOLATE",
		0x2067: "RIGHT-TO-LEFT ISOLATE", 0x2068: "FIRST STRONG ISOLATE", 0x2069: "POP DIRECTIONAL ISOLATE",
		0x206a: "INHIBIT SYMMETRIC SWAPPING", 0x206b: "ACTIVATE SYMMETRIC SWAPPING",
		0x206c: "INHIBIT ARABIC FORM SHAPING", 0x206d: "ACTIVATE ARABIC FORM SHAPING",
		0x206e: "NATIONAL DIGIT SHAPES", 0x206f: "NOMINAL DIGIT SHAPES", 0x00a0: "NO-BREAK SPACE",
		0x1680: "OGHAM SPACE MARK", 0x2000: "EN QUAD", 0x2001: "EM QUAD",
		0x2002: "EN SPACE", 0x2003: "EM SPACE", 0x2004: "THREE-PER-EM SPACE",
		0x2005: "FOUR-PER-EM SPACE", 0x2006: "SIX-PER-EM SPACE", 0x2007: "FIGURE SPACE",
		0x2008: "PUNCTUATION SPACE", 0x2009: "THIN SPACE", 0x200a: "HAIR SPACE",
		0x202f: "NARROW NO-BREAK SPACE", 0x205f: "MEDIUM MATHEMATICAL SPACE",
		0x3000: "IDEOGRAPHIC SPACE", 0xfeff: "ZERO WIDTH NO-BREAK SPACE",
		0x3164: "HANGUL FILLER", 0xffa0: "HALFWIDTH HANGUL FILLER", 0xfff9: "INTERLINEAR ANNOTATION ANCHOR",
		0xfffa: "INTERLINEAR ANNOTATION SEPARATOR", 0xfffb: "INTERLINEAR ANNOTATION TERMINATOR",
	}
	n := name[r]
	if n == "" && r >= 0xfe00 && r <= 0xfe0f {
		n = fmt.Sprintf("VARIATION SELECTOR-%d", r-0xfe00+1)
	}
	if n == "" && r >= 0xe0100 && r <= 0xe01ef {
		n = fmt.Sprintf("VARIATION SELECTOR-%d", r-0xe0100+17)
	}
	if n == "" {
		tagNames := map[rune]string{
			0xe0001: "LANGUAGE TAG", 0xe0020: "TAG SPACE", 0xe0041: "TAG LATIN CAPITAL LETTER A",
			0xe007f: "CANCEL TAG",
		}
		n = tagNames[r]
	}
	if n == "" {
		switch r {
		case 0x13430:
			n = "EGYPTIAN HIEROGLYPH VERTICAL JOINER"
		case 0x13431:
			n = "EGYPTIAN HIEROGLYPH HORIZONTAL JOINER"
		case 0x13432:
			n = "EGYPTIAN HIEROGLYPH INSERT AT TOP START"
		case 0x13433:
			n = "EGYPTIAN HIEROGLYPH INSERT AT BOTTOM START"
		case 0x13434:
			n = "EGYPTIAN HIEROGLYPH INSERT AT TOP END"
		case 0x13435:
			n = "EGYPTIAN HIEROGLYPH INSERT AT BOTTOM END"
		case 0x13436:
			n = "EGYPTIAN HIEROGLYPH OVERLAY MIDDLE"
		case 0x13437:
			n = "EGYPTIAN HIEROGLYPH BEGIN SEGMENT"
		case 0x13438:
			n = "EGYPTIAN HIEROGLYPH END SEGMENT"
		case 0x13439:
			n = "EGYPTIAN HIEROGLYPH INSERT AT MIDDLE"
		case 0x1343a:
			n = "EGYPTIAN HIEROGLYPH INSERT AT TOP"
		case 0x1343b:
			n = "EGYPTIAN HIEROGLYPH INSERT AT BOTTOM"
		case 0x1343c:
			n = "EGYPTIAN HIEROGLYPH BEGIN ENCLOSURE"
		case 0x1343d:
			n = "EGYPTIAN HIEROGLYPH END ENCLOSURE"
		case 0x1343e:
			n = "EGYPTIAN HIEROGLYPH BEGIN WALLED ENCLOSURE"
		case 0x1343f:
			n = "EGYPTIAN HIEROGLYPH END WALLED ENCLOSURE"
		case 0x1bca0:
			n = "SHORTHAND FORMAT LETTER OVERLAP"
		case 0x1bca1:
			n = "SHORTHAND FORMAT CONTINUING OVERLAP"
		case 0x1bca2:
			n = "SHORTHAND FORMAT DOWN STEP"
		case 0x1bca3:
			n = "SHORTHAND FORMAT UP STEP"
		case 0x1d173:
			n = "MUSICAL SYMBOL BEGIN BEAM"
		case 0x1d174:
			n = "MUSICAL SYMBOL END BEAM"
		case 0x1d175:
			n = "MUSICAL SYMBOL BEGIN TIE"
		case 0x1d176:
			n = "MUSICAL SYMBOL END TIE"
		case 0x1d177:
			n = "MUSICAL SYMBOL BEGIN SLUR"
		case 0x1d178:
			n = "MUSICAL SYMBOL END SLUR"
		case 0x1d179:
			n = "MUSICAL SYMBOL BEGIN PHRASE"
		case 0x1d17a:
			n = "MUSICAL SYMBOL END PHRASE"
		}
	}
	if n == "" {
		n = "UNKNOWN"
	}
	return fmt.Sprintf("U+%04X %s (%s)", r, n, unicodeCategory(r))
}

func unicodeCategory(r rune) string {
	for _, name := range []string{
		"Cc", "Cf", "Cn", "Co", "Cs", "Ll", "Lm", "Lo", "Lt", "Lu",
		"Mc", "Me", "Mn", "Nd", "Nl", "No", "Pc", "Pd", "Pe", "Pf",
		"Pi", "Po", "Ps", "Sc", "Sk", "Sm", "So", "Zl", "Zp", "Zs",
	} {
		if table := unicode.Categories[name]; table != nil && unicode.Is(table, r) {
			return name
		}
	}
	return "Cn"
}

func hitConfidence(kind string) string {
	if kind == "space" {
		return "informational"
	}
	return "probable"
}

func inspectText(data []byte, aggressive, stripEmojiGlue bool) TextReport {
	units := decodeTextUnits(data)
	flags, embeddings := validFlagTags(units), validBidiEmbeddings(units)
	type bucket struct {
		cp, kind int
		offsets  []int
	}
	buckets := map[[2]int][]int{}
	var prevKept *rune
	for i, u := range units {
		var prev, next *rune
		if i > 0 {
			prev = &units[i-1].r
		}
		if i+1 < len(units) {
			next = &units[i+1].r
		}
		action, out, kind := decide(u, prevKept, prev, next, flags[i], embeddings[i], true, aggressive, stripEmojiGlue, true)
		if kind == "" {
			if !isGlue(u.r) {
				if !u.valid {
					prevKept = &u.r
				} else {
					r := []rune(out)
					if len(r) > 0 {
						prevKept = &r[0]
					}
				}
			}
			continue
		}
		key := [2]int{int(u.r), kindID(kind)}
		buckets[key] = append(buckets[key], i)
		if action == "replace" {
			r := []rune(out)
			if len(r) > 0 {
				prevKept = &r[0]
			}
		}
	}
	type sortedHit struct {
		cp, kind int
		offsets  []int
	}
	hits := make([]sortedHit, 0, len(buckets))
	for key, offsets := range buckets {
		hits = append(hits, sortedHit{key[0], key[1], offsets})
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].offsets) != len(hits[j].offsets) {
			return len(hits[i].offsets) > len(hits[j].offsets)
		}
		return hits[i].cp < hits[j].cp
	})
	reportHits := make([]TextHit, 0, len(hits))
	total := 0
	for _, h := range hits {
		kind := kindName(h.kind)
		offsets := append([]int(nil), h.offsets...)
		if len(offsets) > 10 {
			offsets = offsets[:10]
		}
		reportHits = append(reportHits, TextHit{Codepoint: fmt.Sprintf("U+%04X", h.cp), Label: runeLabel(rune(h.cp)), Count: len(h.offsets), Kind: kind, Confidence: hitConfidence(kind), SampleOffset: offsets})
		total += len(h.offsets)
	}
	notes := []string{
		"Layer A only: invisible/format Unicode and space homoglyphs (edit-based carriers).",
		"Statistical (token-sampling) watermarks are not detectable here; use Layer B rewrite.",
		"Inspect kinds: strip, bidi, tag_chars, variation_selector, zwj_family, private_use, space, confusable, other_cf.",
		"Load-bearing invisibles are preserved by default during cleaning: emoji glue, CJK/Mongolian variation selectors, script joiners, complete flag tag sequences, same-script fillers/selectors (Mongolian FVS, Khmer inherent vowels, Hangul jamo fillers), RTL directional marks/paired embeddings, orthographic Arabic/Syriac Cf marks, and visible-layout format controls next to their own script (Egyptian hieroglyph quadrat, Duployan shorthand, musical beaming). Inspection still reports bidi controls. Use explicit strip flags only after review.",
	}
	if len(reportHits) == 0 {
		notes = append(notes, "No deterministic Layer A (invisible Unicode/format) carriers detected; statistical and pixel-domain marks are out of scope here.")
	}
	return TextReport{Length: len(units), SuspiciousTotal: total, Hits: reportHits, Notes: notes}
}

var kindIDs = map[string]int{"strip": 1, "bidi": 2, "tag_chars": 3, "variation_selector": 4, "zwj_family": 5, "private_use": 6, "noncharacter": 7, "reserved_ignorable": 8, "space": 9, "confusable": 10, "other_cf": 11}

func kindID(s string) int { return kindIDs[s] }
func kindName(id int) string {
	for name, value := range kindIDs {
		if value == id {
			return name
		}
	}
	return "strip"
}

// countNFKCChangedRunes counts input codepoints that are part of a changed
// normalization span. Python's reference implementation uses
// SequenceMatcher and counts the source side of non-equal opcodes. For the
// short/medium strings normally passed through Layer A, the matching-block
// algorithm below reproduces that behavior. Very large strings use a
// conservative prefix/suffix fallback so a pathological normalization cannot
// turn cleaning into an O(n²) memory operation.
func countNFKCChangedRunes(before, after string) int {
	a, b := []rune(before), []rune(after)
	if len(a) == 0 {
		return 0
	}
	// Python's reference uses difflib.SequenceMatcher(None, before, after,
	// autojunk=False), not a minimal LCS diff.  For bounded inputs reproduce
	// its leftmost-longest matching-block algorithm exactly; this matters for
	// repeated characters where the two algorithms count different source
	// spans as changed (for example, "aba" -> "bca").
	const sequenceMatcherCells int64 = 4_000_000
	if int64(len(a))*int64(len(b)) <= sequenceMatcherCells {
		return sequenceMatcherChangedRunes(a, b)
	}

	// Keep the normalization path bounded for pathological input sizes.  The
	// fallback is conservative: it counts the changed middle span rather than
	// allocating a quadratic diff table.
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	aEnd, bEnd := len(a), len(b)
	for aEnd > start && bEnd > start && a[aEnd-1] == b[bEnd-1] {
		aEnd--
		bEnd--
	}
	if aEnd == start {
		return 0
	}
	return aEnd - start
}

// sequenceMatcherChangedRunes returns the number of source-side runes that
// Python's difflib.SequenceMatcher marks as non-equal when autojunk is off.
// It is deliberately limited to the behavior needed by countNFKCChangedRunes:
// there is no junk predicate and only the source-side changed count matters.
func sequenceMatcherChangedRunes(a, b []rune) int {
	type region struct {
		alo, ahi, blo, bhi int
	}
	type block struct {
		i, j, size int
	}

	b2j := make(map[rune][]int, len(b))
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}

	findLongest := func(alo, ahi, blo, bhi int) (int, int, int) {
		besti, bestj, bestsize := alo, blo, 0
		j2len := make(map[int]int)
		for i := alo; i < ahi; i++ {
			newJ2Len := make(map[int]int)
			for _, j := range b2j[a[i]] {
				if j < blo {
					continue
				}
				if j >= bhi {
					break
				}
				k := j2len[j-1] + 1
				newJ2Len[j] = k
				if k > bestsize {
					besti, bestj, bestsize = i-k+1, j-k+1, k
				}
			}
			j2len = newJ2Len
		}
		// SequenceMatcher has no junk predicate here, so the two extension
		// phases are simply the matching non-junk phases.
		for besti > alo && bestj > blo && a[besti-1] == b[bestj-1] {
			besti--
			bestj--
			bestsize++
		}
		for besti+bestsize < ahi && bestj+bestsize < bhi && a[besti+bestsize] == b[bestj+bestsize] {
			bestsize++
		}
		return besti, bestj, bestsize
	}

	queue := []region{{alo: 0, ahi: len(a), blo: 0, bhi: len(b)}}
	blocks := []block{}
	for len(queue) > 0 {
		last := len(queue) - 1
		current := queue[last]
		queue = queue[:last]
		i, j, size := findLongest(current.alo, current.ahi, current.blo, current.bhi)
		if size == 0 {
			continue
		}
		blocks = append(blocks, block{i: i, j: j, size: size})
		if current.alo < i && current.blo < j {
			queue = append(queue, region{alo: current.alo, ahi: i, blo: current.blo, bhi: j})
		}
		if i+size < current.ahi && j+size < current.bhi {
			queue = append(queue, region{alo: i + size, ahi: current.ahi, blo: j + size, bhi: current.bhi})
		}
	}

	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].i != blocks[j].i {
			return blocks[i].i < blocks[j].i
		}
		if blocks[i].j != blocks[j].j {
			return blocks[i].j < blocks[j].j
		}
		return blocks[i].size < blocks[j].size
	})
	matched := 0
	lastI, lastJ, lastSize := 0, 0, 0
	for _, current := range blocks {
		if lastI+lastSize == current.i && lastJ+lastSize == current.j {
			lastSize += current.size
			continue
		}
		if lastSize > 0 {
			matched += lastSize
		}
		lastI, lastJ, lastSize = current.i, current.j, current.size
	}
	if lastSize > 0 {
		matched += lastSize
	}
	return len(a) - matched
}

func cleanText(data []byte, opts Options) ([]byte, map[string]any) {
	units := decodeTextUnits(data)
	flags, embeddings := validFlagTags(units), validBidiEmbeddings(units)
	removed := map[string]int{}
	replaced := map[string]int{}
	out := make([]byte, 0, len(data))
	var prevKept *rune
	for i, u := range units {
		var prev, next *rune
		if i > 0 {
			prev = &units[i-1].r
		}
		if i+1 < len(units) {
			next = &units[i+1].r
		}
		action, replacement, _ := decide(u, prevKept, prev, next, flags[i], embeddings[i], opts.NormalizeSpaces, opts.AggressiveHomoglyphs, opts.StripEmojiGlue, opts.StripBidi)
		switch action {
		case "keep":
			out = append(out, []byte(replacement)...)
			if !isGlue(u.r) {
				if !u.valid {
					prevKept = &u.r
				} else {
					r := []rune(replacement)
					if len(r) > 0 {
						prevKept = &r[0]
					}
				}
			}
		case "replace":
			out = append(out, []byte(replacement)...)
			replaced[runeLabel(u.r)]++
			r := []rune(replacement)
			if len(r) > 0 {
				prevKept = &r[0]
			}
		default:
			removed[runeLabel(u.r)]++
		}
	}
	nfkcChanged := false
	if opts.NFKC {
		before := string(out)
		normalized := norm.NFKC.String(before)
		if normalized != before {
			nfkcChanged = true
			changedInputs := countNFKCChangedRunes(before, normalized)
			if changedInputs == 0 {
				changedInputs = 1
			}
			replaced["NFKC_normalize"] += changedInputs
			out = []byte(normalized)
		}
	}
	return out, map[string]any{
		"input_length": len(units), "output_length": utf8.RuneCount(out),
		"removed": removed, "replaced": replaced,
		"removed_count": sumInts(removed), "replaced_count": sumInts(replaced), "nfkc_changed": nfkcChanged,
	}
}

func sumInts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func humanTextReport(report TextReport) string {
	lines := []string{fmt.Sprintf("Length: %d chars", report.Length), fmt.Sprintf("Suspicious: %d", report.SuspiciousTotal)}
	if len(report.Hits) > 0 {
		lines = append(lines, "Hits:")
		for _, h := range report.Hits {
			lines = append(lines, fmt.Sprintf("  [%s/%s] %s x%d @ %v", h.Kind, h.Confidence, h.Label, h.Count, h.SampleOffset))
		}
	}
	for _, n := range report.Notes {
		lines = append(lines, "Note: "+n)
	}
	return strings.Join(lines, "\n")
}
