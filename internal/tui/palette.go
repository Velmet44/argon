// Package tui design system — one palette, one spacing rule, everywhere.
//
// Spacing rule: exactly one blank line between blocks, never trailing
// whitespace lines. Panels pad 0 1 (no vertical padding — the blank
// separator lines own the rhythm).
//
// Color roles (fixed, readable on dark AND light terminals):
//   - argon voice   : cyan, italic        (thinking indicator, reasoning)
//   - thought body  : violet              (expanded reasoning, distinct + calm)
//   - command       : bold yellow, yellow edge  (the thing about to run)
//   - live output   : default fg, grey edge    (the terminal talking back)
//   - final message : default fg, green tick   (the answer)
//   - timing/meta   : dim grey            (seconds, tiers, hints)
//   - error         : bold red            (blocks, failures)
//   - user echo     : bold blue           (your prompt, section heads)
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

// --- palette ---------------------------------------------------------------

var (
	cCyan    = lipgloss.Color("6")
	cViolet  = lipgloss.Color("99")
	cYellow  = lipgloss.Color("11")
	cEdgeY   = lipgloss.Color("3")
	cGreen   = lipgloss.Color("10")
	cRed     = lipgloss.Color("9")
	cBlue    = lipgloss.Color("12")
	cGrey    = lipgloss.Color("8")
	hoverBG  = lipgloss.AdaptiveColor{Dark: "236", Light: "254"}
)

// --- primitive styles ------------------------------------------------------

func stArgon(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Italic(true)
	if color {
		s = s.Foreground(cCyan)
	}
	return s
}

func stThoughtBody(color bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if color {
		s = s.Foreground(cViolet)
	}
	return s
}

func stThoughtHead(color, hovered bool) lipgloss.Style {
	s := lipgloss.NewStyle().Italic(true)
	if color {
		s = s.Foreground(cGrey)
		if hovered {
			s = s.Background(hoverBG).Foreground(cCyan).Underline(true)
		}
	}
	return s
}

func stCmd(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	if color {
		s = s.Foreground(cYellow)
	}
	return s
}

func stEdgeCmd(color bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if color {
		s = s.Foreground(cEdgeY)
	}
	return s
}

func stEdgeOut(color bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if color {
		s = s.Foreground(cGrey)
	}
	return s
}

func stMeta(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Faint(true)
	if color {
		s = s.Foreground(cGrey)
	}
	return s
}

func stErr(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	if color {
		s = s.Foreground(cRed)
	}
	return s
}

func stHead(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	if color {
		s = s.Foreground(cBlue)
	}
	return s
}

func stDone(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	if color {
		s = s.Foreground(cGreen)
	}
	return s
}

// --- block renderers (each returns its lines, no trailing blank) -----------

// edge prefixes a left rule to every line: "│ ".
func edge(lines []string, color bool, edgeStyle lipgloss.Style) []string {
	bar := edgeStyle.Render("│ ")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, bar+l)
	}
	return out
}

// wrap folds text to width (min 20 to survive tiny windows).
func wrap(text string, width int) []string {
	w := width - 4
	if w < 20 {
		w = 20
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	return strings.Split(wordwrap.String(text, w), "\n")
}

// thoughtHeader renders the collapsed line: "◈ thought for 1.2s".
func thoughtHeader(color, hovered bool, elapsed string) string {
	label := "◈ thought for " + elapsed
	if hovered {
		label += "  · click to expand"
	}
	return stThoughtHead(color, hovered).Render(label)
}

// thoughtBody renders expanded reasoning in violet with a matching edge.
func thoughtBody(color bool, text string, width int) []string {
	body := wrap(text, width)
	styled := make([]string, 0, len(body))
	bs := stThoughtBody(color)
	for _, l := range body {
		styled = append(styled, bs.Render(l))
	}
	head := stThoughtHead(color, false).Render("◈ thought · click text to collapse")
	return append([]string{head}, edge(styled, color, edgeStyleViolet(color))...)
}

func edgeStyleViolet(color bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if color {
		s = s.Foreground(cViolet)
	}
	return s
}

// commandBlock renders "$ cmd" bold yellow with a yellow edge + tier tag.
func commandBlock(color bool, cmd, tier string, width int) []string {
	head := stCmd(color).Render("$ "+cmd) + "  " + stMeta(color).Render("["+tier+"]")
	return edge([]string{head}, color, stEdgeCmd(color))
}

// outputLines renders terminal output: default fg, grey edge, no wrapping
// (terminal output keeps its own columns; long lines scroll).
func outputLines(color bool, lines []string) []string {
	return edge(lines, color, stEdgeOut(color))
}

// messageLine renders the final answer with a green tick.
func messageLine(color bool, text string, width int) []string {
	tick := stDone(color).Render("✓ ")
	out := []string{}
	for i, l := range wrap(text, width) {
		if i == 0 {
			out = append(out, tick+l)
		} else {
			out = append(out, "  "+l)
		}
	}
	return out
}

// thinkingLine renders the live "Thinking… Ns" indicator.
func thinkingLine(color bool, elapsed string) string {
	return stArgon(color).Render("◈ Thinking… "+elapsed)
}
