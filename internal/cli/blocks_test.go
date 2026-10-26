package cli

import (
	"strings"
	"testing"
)

func TestEdgeWriterSplitsChunks(t *testing.T) {
	w := newEdgeWriter(false, 80)
	w.Write("a\nb")
	w.Write("c\npartial")
	w.Flush()
	// should not panic; partial line flushed on Flush
	if w.n != 3 {
		t.Fatalf("want 3 lines, got %d", w.n)
	}
	if w.empty {
		t.Fatal("not empty after writes")
	}
}

func TestEdgeWriterEmpty(t *testing.T) {
	w := newEdgeWriter(false, 80)
	w.Flush() // prints "(no output)", must not panic
	if !w.empty {
		t.Fatal("still empty")
	}
}

func TestEdgeWriterCRLF(t *testing.T) {
	w := newEdgeWriter(false, 80)
	w.Write("a\r\nb\r\n")
	w.Flush()
	if w.n != 2 {
		t.Fatalf("want 2 lines, got %d", w.n)
	}
}

func TestLLMHintMapping(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"llm http 401: nope", "key"},
		{"llm http 404: nope", "not served"},
		{"llm http 410: gone", "end-of-life"},
		{"llm http 429: slow", "Rate limited"},
		{"context deadline exceeded", "timed out"},
		{"weird thing", "weird thing"},
	} {
		got := strings.Join(llmHint(testErr(tc.in)), " ")
		if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.want)) {
			t.Errorf("%q → %q, want %q", tc.in, got, tc.want)
		}
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }
