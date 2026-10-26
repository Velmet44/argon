package tui

import (
	"strings"
	"testing"
)

func TestBlockSpansAndFind(t *testing.T) {
	blocks := []*rBlock{
		{kind: rEcho, text: "hi"},
		{kind: rThought, thought: &ThoughtBlock{Done: true, Elapsed: 1_200_000_000, Text: "t", Expanded: false}},
		{kind: rCmd, cmd: "echo hi", tier: "read"},
	}
	spans, total := blockSpans(blocks, false, 80)
	if len(spans) != 3 || spans[0] != 0 {
		t.Fatalf("spans %v total %d", spans, total)
	}
	// echo(1) + sep + thought collapsed(1) + sep + cmd(1) = 5
	if total != 5 {
		t.Fatalf("total %d", total)
	}
	if got := findBlock(spans, total, 0); got != 0 {
		t.Errorf("line0 -> %d", got)
	}
	if got := findBlock(spans, total, 2); got != 1 {
		t.Errorf("line2 -> %d", got)
	}
	if got := findBlock(spans, total, 4); got != 2 {
		t.Errorf("line4 -> %d", got)
	}
}

func TestToggleLastCollapsible(t *testing.T) {
	blocks := []*rBlock{
		{kind: rThought, thought: &ThoughtBlock{Done: true, Text: "t"}},
		{kind: rOut, out: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}},
	}
	if !toggleLastCollapsible(blocks) {
		t.Fatal("want toggle")
	}
	if !blocks[1].outOpen {
		t.Fatal("out should open first (newest)")
	}
	if !toggleLastCollapsible(blocks) {
		t.Fatal("want toggle")
	}
	if blocks[1].outOpen {
		t.Fatal("out should close")
	}
	blocks[1].out = []string{"short"}
	if !toggleLastCollapsible(blocks) {
		t.Fatal("want toggle")
	}
	if !blocks[0].thought.Expanded {
		t.Fatal("small out skipped, thought toggles")
	}
}

func TestOutCollapseRender(t *testing.T) {
	b := &rBlock{kind: rOut}
	for i := 0; i < 12; i++ {
		b.out = append(b.out, "line")
	}
	lines := b.render(false, 80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "4 more lines") {
		t.Fatalf("want preview footer: %q", joined)
	}
	b.outOpen = true
	if len(b.render(false, 80)) != 12 {
		t.Fatal("expanded shows all")
	}
}
