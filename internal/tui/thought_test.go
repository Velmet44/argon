package tui

import (
	"testing"
	"time"
)

func TestFindBlock(t *testing.T) {
	// spans [0,1,4], total 6 -> lines 0 | 1..3 | 4..5
	spans := []int{0, 1, 4}
	for line, want := range map[int]int{0: 0, 1: 1, 3: 1, 4: 2, 5: 2, 6: -1, -1: -1} {
		if got := findBlock(spans, 6, line); got != want {
			t.Errorf("line %d: got %d want %d", line, got, want)
		}
	}
}

func TestThoughtLineCount(t *testing.T) {
	th := &ThoughtBlock{StartedAt: time.Now()}
	if th.lineCount(80) != 1 {
		t.Fatal("thinking should be 1 line")
	}
	th.Done, th.Elapsed = true, 1200*time.Millisecond
	if th.lineCount(80) != 1 {
		t.Fatal("collapsed should be 1 line")
	}
	th.Text = "short thought"
	th.Expanded = true
	if th.lineCount(80) != 2 { // header + 1 body line
		t.Fatalf("expanded got %d", th.lineCount(80))
	}
}
