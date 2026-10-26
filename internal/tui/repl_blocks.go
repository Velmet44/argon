package tui

import "fmt"

// REPL transcript blocks — same visual language as the one-shot plain output
// (palette.go), but collapsible: thoughts start collapsed, long outputs show
// a preview. Click (or F2) toggles either.
type rKind int

const (
	rEcho rKind = iota
	rHead
	rThought
	rCmd
	rOut
	rMsg
	rErr
	rMeta
)

// outPreview caps collapsed command output before "click to view".
const outPreview = 8

type rBlock struct {
	kind     rKind
	text     string // echo/head/msg/err/meta
	thought  *ThoughtBlock
	cmd      string
	tier     string
	out      []string
	outOpen  bool
	outHover bool
}

func (b *rBlock) render(color bool, width int) []string {
	switch b.kind {
	case rEcho:
		return PromptEcho(color, b.text, width)
	case rHead:
		return []string{stHead(color).Render(b.text)}
	case rThought:
		return b.thought.lines(color, width)
	case rCmd:
		return CommandLines(color, b.cmd, b.tier, width)
	case rOut:
		if b.outOpen || len(b.out) <= outPreview {
			return outputLines(color, b.out)
		}
		shown := outputLines(color, b.out[:outPreview])
		more := fmt.Sprintf("… %d more lines — click to view", len(b.out)-outPreview)
		if b.outHover {
			return append(shown, stThoughtHead(color, true).Render(more))
		}
		return append(shown, stMeta(color).Render(more))
	case rMsg:
		return AnswerLines(color, b.text, width)
	case rErr:
		out := []string{}
		for _, l := range wrap(b.text, width) {
			out = append(out, stErr(color).Render(l))
		}
		return out
	case rMeta:
		return []string{stMeta(color).Render(b.text)}
	}
	return nil
}

func (b *rBlock) lineCount(color bool, width int) int { return len(b.render(color, width)) }

// blockSpans maps each block to its starting absolute line (blank separators
// count). Mirrors the deleted one-shot runner's spans logic.
func blockSpans(blocks []*rBlock, color bool, width int) ([]int, int) {
	spans := make([]int, 0, len(blocks))
	line := 0
	for i, b := range blocks {
		spans = append(spans, line)
		line += b.lineCount(color, width)
		if i != len(blocks)-1 {
			line++
		}
	}
	return spans, line
}

func isThoughtBlock(b *rBlock) bool {
	return b != nil && b.kind == rThought && b.thought.Done
}

func isOutBlock(b *rBlock) bool { return b != nil && b.kind == rOut }

// toggleLastCollapsible flips the newest collapsible block (thought or output).
func toggleLastCollapsible(blocks []*rBlock) bool {
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if isThoughtBlock(b) {
			b.thought.Expanded = !b.thought.Expanded
			return true
		}
		if isOutBlock(b) && len(b.out) > outPreview {
			b.outOpen = !b.outOpen
			return true
		}
	}
	return false
}

func clearHover(blocks []*rBlock) bool {
	changed := false
	for _, b := range blocks {
		if b.kind == rThought && b.thought.Hovered {
			b.thought.Hovered = false
			changed = true
		}
		if b.kind == rOut && b.outHover {
			b.outHover = false
			changed = true
		}
	}
	return changed
}
