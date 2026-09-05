package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	DefaultGumbelWindow    = 4
	DefaultGumbelThreshold = 1e-6
)

var simpleGumbelTokenRE = regexp.MustCompile("[A-Za-z0-9]+")

// TokenizeSimpleGumbel returns the deterministic convenience tokenization used
// by the upstream detector. Exact replay against a generator still requires
// passing that generator's token IDs to DetectGumbelTokenIDs.
func TokenizeSimpleGumbel(text string) []uint64 {
	matches := simpleGumbelTokenRE.FindAllString(strings.ToLower(text), -1)
	ids := make([]uint64, len(matches))
	for i, token := range matches {
		digest := sha256.Sum256([]byte(token))
		ids[i] = binary.BigEndian.Uint64(digest[:8])
	}
	return ids
}

// LoadGumbelTokenIDs parses either a JSON array of integer token IDs or one
// integer per line. Decimal and 0x-prefixed values are accepted per line.
func LoadGumbelTokenIDs(raw []byte) ([]uint64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var values []json.Number
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return nil, fmt.Errorf("token-id JSON must be an array of integers: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				return nil, errors.New("token-id JSON must contain exactly one array")
			}
			return nil, fmt.Errorf("token-id JSON has trailing data: %w", err)
		}
		ids := make([]uint64, len(values))
		for i, value := range values {
			parsed, err := parseGumbelID(value.String())
			if err != nil {
				return nil, fmt.Errorf("token id %d: %w", i, err)
			}
			ids[i] = parsed
		}
		return ids, nil
	}
	ids := []uint64{}
	for lineNumber, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, err := parseGumbelID(line)
		if err != nil {
			return nil, fmt.Errorf("token id on line %d: %w", lineNumber+1, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseGumbelID(raw string) (uint64, error) {
	if strings.HasPrefix(raw, "-") {
		return 0, errors.New("must be an unsigned integer")
	}
	base := 10
	value := raw
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		base = 16
		value = value[2:]
	}
	if value == "" {
		return 0, errors.New("must be an unsigned integer")
	}
	id, err := strconv.ParseUint(value, base, 64)
	if err != nil {
		return 0, fmt.Errorf("must be an unsigned 64-bit integer: %w", err)
	}
	return id, nil
}

func normalizeGumbelKey(raw string) ([]byte, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return nil, errors.New("watermark key must not be empty")
	}
	if strings.HasPrefix(key, "0x") || strings.HasPrefix(key, "0X") {
		hexPart := key[2:]
		if hexPart == "" || len(hexPart)%2 != 0 {
			return nil, errors.New("invalid hex key (expected 0x followed by even-length hex)")
		}
		decoded, err := hex.DecodeString(hexPart)
		if err != nil {
			return nil, errors.New("invalid hex key (expected 0x followed by even-length hex)")
		}
		return decoded, nil
	}
	return []byte(key), nil
}

func keyedGumbelSeed(key []byte, window []uint64) []byte {
	packed := make([]byte, 8*len(window))
	for i, token := range window {
		binary.BigEndian.PutUint64(packed[i*8:], token)
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(packed)
	return h.Sum(nil)
}

func keyedGumbelUniform(seed []byte, token uint64) float64 {
	var packed [8]byte
	binary.BigEndian.PutUint64(packed[:], token)
	h := hmac.New(sha256.New, seed)
	_, _ = h.Write(packed[:])
	digest := h.Sum(nil)
	value := binary.BigEndian.Uint64(digest[:8])
	return math.Ldexp(float64(value)+0.5, -64)
}

func poissonSurvival(s float64, n int) float64 {
	if n <= 0 || s <= 0 {
		return 1
	}
	logS := math.Log(s)
	logTerm := 0.0
	maxValue := 0.0
	for k := 1; k < n; k++ {
		logTerm += logS - math.Log(float64(k))
		if logTerm > maxValue {
			maxValue = logTerm
		}
	}
	logTerm = 0
	accumulator := math.Exp(-maxValue)
	for k := 1; k < n; k++ {
		logTerm += logS - math.Log(float64(k))
		accumulator += math.Exp(logTerm - maxValue)
	}
	p := math.Exp(-s + maxValue + math.Log(accumulator))
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// DetectGumbelTokenIDs runs the model-free keyed-Gumbel (EXP) same-key replay
// test. It is intentionally an evidence report, not a vendor watermark oracle.
func DetectGumbelTokenIDs(tokenIDs []uint64, key string, window int, threshold float64) (map[string]any, error) {
	if window < 1 {
		return nil, errors.New("window must be >= 1")
	}
	if threshold <= 0 || threshold >= 1 {
		return nil, errors.New("threshold must be in (0, 1)")
	}
	keyBytes, err := normalizeGumbelKey(key)
	if err != nil {
		return nil, err
	}
	statistic := 0.0
	counted := 0
	skippedRepeated := 0
	seen := map[string]bool{}
	for index := window; index < len(tokenIDs); index++ {
		context := tokenIDs[index-window : index]
		packed := make([]byte, 8*len(context))
		for i, token := range context {
			binary.BigEndian.PutUint64(packed[i*8:], token)
		}
		windowKey := string(packed)
		if seen[windowKey] {
			skippedRepeated++
			continue
		}
		seen[windowKey] = true
		u := keyedGumbelUniform(keyedGumbelSeed(keyBytes, context), tokenIDs[index])
		statistic += -math.Log1p(-u)
		counted++
	}
	report := map[string]any{
		"detector": "gumbel", "scheme": "exp", "vendor": "self-hosted", "available": true,
		"window": window, "threshold": threshold, "tokens_total": len(tokenIDs),
		"skipped_no_context": minInt(window, len(tokenIDs)), "skipped_repeated": skippedRepeated,
		"counted": counted,
	}
	if counted == 0 {
		report["is_watermarked"] = false
		report["p_value"] = 1.0
		report["score"] = 0.0
		report["note"] = "no verifiable token positions (text too short for a full context window)"
		return report, nil
	}
	p := poissonSurvival(statistic, counted)
	score := 300.0
	if p > 0 {
		score = -math.Log10(p)
	}
	if score > 300 {
		score = 300
	}
	report["statistic"] = math.Round(statistic*1e6) / 1e6
	report["p_value"] = p
	report["score"] = math.Round(score*1e6) / 1e6
	report["is_watermarked"] = p < threshold
	report["note"] = "same-key replay of the keyed-Gumbel (Aaronson EXP) watermark; valid only against the same key, tokenizer, and PRF layout used at generation"
	return report, nil
}

// DetectGumbelText runs the convenience tokenizer path.
func DetectGumbelText(text, key string, window int, threshold float64) (map[string]any, error) {
	return DetectGumbelTokenIDs(TokenizeSimpleGumbel(text), key, window, threshold)
}
