package tui

import (
	"fmt"
	"time"
)

// ThoughtBlock is one collapsible reasoning unit.
//
//	thinking  -> live counter, not clickable yet
//	collapsed -> "◈ thought for 1.2s" (hover highlights, click expands)
//	expanded  -> violet body + "click text to collapse"
type ThoughtBlock struct {
	Text      string
	StartedAt time.Time
	Elapsed   time.Duration // frozen when done
	Done      bool
	Expanded  bool
	Hovered   bool
}

func (t *ThoughtBlock) liveElapsed() time.Duration {
	if t.Done {
		return t.Elapsed
	}
	return time.Since(t.StartedAt)
}

func fmtSecs(d time.Duration) string {
	s := d.Seconds()
	if s < 10 {
		return fmt.Sprintf("%.1fs", s)
	}
	return fmt.Sprintf("%ds", int(s+0.5))
}

// lines renders the block at width (no trailing blank line).
func (t *ThoughtBlock) lines(color bool, width int) []string {
	if !t.Done {
		return []string{thinkingLine(color, fmtSecs(t.liveElapsed()))}
	}
	if !t.Expanded {
		return []string{thoughtHeader(color, t.Hovered, fmtSecs(t.Elapsed))}
	}
	return thoughtBody(color, t.Text, width)
}

// lineCount mirrors lines() without allocating strings (for hit-testing).
func (t *ThoughtBlock) lineCount(width int) int {
	if !t.Done || !t.Expanded {
		return 1
	}
	return 1 + len(wrap(t.Text, width))
}

// findBlock maps an absolute transcript line to a block index.
// spans holds each block's starting line; total is the line count.
func findBlock(spans []int, total, line int) int {
	if line < 0 || line >= total {
		return -1
	}
	for i := len(spans) - 1; i >= 0; i-- {
		if line >= spans[i] {
			return i
		}
	}
	return -1
}
