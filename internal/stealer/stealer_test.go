package stealer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDefaultTokenizeMatchesUpstreamShape(t *testing.T) {
	got := DefaultTokenize("Hello, World! 3.5")
	want := []string{"hello", ",", "world", "!", "3", ".", "5"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
}

func TestCountNGramsAndContextKeyAreBoundarySafe(t *testing.T) {
	counts := CountNGrams([]string{"a b a b"}, 1)
	if counts.Contexts[ContextKey([]string{"a"})]["b"] != 2 {
		t.Fatalf("a->b counts = %#v", counts.Contexts)
	}
	if counts.Contexts[ContextKey([]string{"b"})]["a"] != 1 || counts.Totals[ContextKey([]string{"b"})] != 1 {
		t.Fatalf("b context = %#v, totals=%#v", counts.Contexts, counts.Totals)
	}
	if counts.Unigrams["a"] != 2 || counts.Unigrams["b"] != 2 {
		t.Fatalf("unigrams = %#v", counts.Unigrams)
	}
	if ContextKey([]string{"a b", "c"}) == ContextKey([]string{"a", "b c"}) {
		t.Fatal("context keys collide")
	}
}

func TestBuildScoreAndApplyDelta(t *testing.T) {
	watermarked := []string{
		"the quick brown fox jumps over the lazy dog . " + strings.Repeat("the quick brown fox jumps over the lazy dog . ", 20),
		"a red fox ran through the green field . " + strings.Repeat("a red fox ran through the green field . ", 20),
	}
	baseline := []string{
		"the lazy dog sleeps by the fire all day . " + strings.Repeat("the lazy dog sleeps by the fire all day . ", 20),
		"a blue car drove across the bridge slowly . " + strings.Repeat("a blue car drove across the bridge slowly . ", 20),
	}
	scorer := BuildScorer(CountNGrams(watermarked, 3), CountNGrams(baseline, 3), 3, 20, 0.4, 1)
	context := ContextKey([]string{"the", "quick", "brown"})
	entries, ok := scorer["scorer"].(map[string]any)[context].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("missing scorer context: %#v", scorer)
	}
	foxScore, found := scoreEntries(entries, "fox")
	if !found || foxScore <= 0 {
		t.Fatalf("fox score = %v, found=%t", foxScore, found)
	}
	adjusted := ApplyDelta(scorer, []string{"the", "quick", "brown"}, map[string]float64{"fox": 0, "cat": 0}, 1)
	if adjusted["fox"] >= 0 || adjusted["cat"] != 0 {
		t.Fatalf("adjusted logits = %#v", adjusted)
	}
	sequence := ScoreSequence(scorer, DefaultTokenize("the quick brown fox jumps"), 3)
	if sequence["applied"] == 0 {
		t.Fatalf("sequence score = %#v", sequence)
	}
}

func TestQueryDryRunWritesOrderedJSONL(t *testing.T) {
	dir := t.TempDir()
	prompts := filepath.Join(dir, "prompts.jsonl")
	out := filepath.Join(dir, "replies.jsonl")
	if err := os.WriteFile(prompts, []byte("{\"text\":\"first\"}\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Query(QueryOptions{Prompts: prompts, Out: out, Backend: "dry-run", Concurrency: 2, MaxNewTokens: 1, Progress: io.Discard}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("reply lines = %d, data=%q", len(lines), data)
	}
	var first map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["prompt"] != "first" || first["reply"] != DryReplyHead+"first" {
		t.Fatalf("first reply = %#v", first)
	}
}

func TestDownloadPromptsPagesAndResumeState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		rows := []any{}
		for i := offset; i < offset+2; i++ {
			rows = append(rows, map[string]any{"row": map[string]any{"text": "prompt-" + strconv.Itoa(i)}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"num_rows_per_page": 2, "rows": rows})
	}))
	defer server.Close()

	outDir := filepath.Join(t.TempDir(), "prompts")
	written, err := DownloadPrompts(DownloadOptions{
		Dataset: "dataset", Config: "config", Split: "train", Count: 3, Field: "text",
		Out: outDir, BaseURL: server.URL, Progress: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if written != 3 {
		t.Fatalf("written = %d", written)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "prompts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("prompt lines = %d, data=%q", len(lines), data)
	}
	var state downloadState
	stateData, err := os.ReadFile(filepath.Join(outDir, ".download-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatal(err)
	}
	if state.NextOffset != 4 || state.Written != 3 {
		t.Fatalf("state = %#v", state)
	}
}

func TestDownloadPromptsCancelsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := DownloadPrompts(DownloadOptions{
			Dataset: "dataset", Config: "config", Split: "train", Count: 1, Field: "text",
			Out: filepath.Join(t.TempDir(), "prompts"), BaseURL: server.URL,
			Context: ctx, Progress: io.Discard,
		})
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("download request did not start")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("cancellation error = %v, want ErrInterrupted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("download did not cancel in-flight request")
	}
}
