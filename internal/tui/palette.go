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
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"golang.org/x/term"
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
	return stArgon(color).Render("◈ Thinking… " + elapsed)
}

// --- shared plain-print blocks (sequential stdout, no viewport) ------------
//
// TermWidth is the box measure: real stdout width clamped to 40–100 so
// transcripts stay readable on ultra-wide terminals and intact on narrow ones.
func TermWidth() int {
	w := 80
	if cols, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && cols > 0 {
		w = cols
	} else if c := strings.TrimSpace(os.Getenv("COLUMNS")); c != "" {
		var n int
		if _, err := fmt.Sscanf(c, "%d", &n); err == nil && n > 0 {
			w = n
		}
	}
	return clampWidth(w)
}

func clampWidth(width int) int {
	if width < 40 {
		return 40
	}
	if width > 100 {
		return 100
	}
	return width
}

func boxBorder(color bool) lipgloss.Style {
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if color {
		s = s.BorderForeground(cGrey)
	}
	return s
}

// ThoughtBoxLines renders the always-visible reasoning box:
// title "◈ thought N · Ns", violet wrapped body, grey rounded border.
func ThoughtBoxLines(color bool, n int, elapsed time.Duration, thought string, width int) []string {
	w := clampWidth(width) - 4 // border (2) + padding (2)
	title := stArgon(color).Render(fmt.Sprintf("◈ thought %d · %s", n, fmtSecs(elapsed)))
	body := wrap(thought, w)
	styled := make([]string, 0, len(body)+1)
	styled = append(styled, title)
	bs := stThoughtBody(color)
	for _, l := range body {
		styled = append(styled, bs.Render(l))
	}
	return strings.Split(boxBorder(color).Render(strings.Join(styled, "\n")), "\n")
}

// CommandLines renders "$ cmd [tier]" bold yellow with a yellow edge.
// Long commands hard-wrap with a yellow "↳ " continuation (tier rides last).
func CommandLines(color bool, cmd, tier string, width int) []string {
	content := clampWidth(width) - 4 // "│ "(2) + "$ "(2)
	chunks := wrap(cmd, content)
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	out := make([]string, 0, len(chunks))
	head := stCmd(color)
	for i, c := range chunks {
		var line string
		if i == 0 {
			line = head.Render("$ " + c)
		} else {
			line = head.Render("↳ " + c)
		}
		if i == len(chunks)-1 {
			line += "  " + stMeta(color).Render("["+tier+"]")
		}
		out = append(out, line)
	}
	return edge(out, color, stEdgeCmd(color))
}

// ErrBoxLines renders a red bordered failure box with title + detail lines.
func ErrBoxLines(color bool, title string, lines []string, width int) []string {
	w := clampWidth(width) - 4
	titleLine := stErr(color).Render("✖ " + title)
	styled := []string{titleLine}
	for _, l := range lines {
		styled = append(styled, wrap(l, w)...)
	}
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if color {
		s = s.BorderForeground(cRed)
	}
	return strings.Split(s.Render(strings.Join(styled, "\n")), "\n")
}

// AnswerLines renders the final answer with a green tick.
func AnswerLines(color bool, text string, width int) []string {
	return messageLine(color, text, width)
}

// MetaLine renders a dim provenance note: "(exit 0 · 0.4s)".
func MetaLine(color bool, text string) string {
	return stMeta(color).Render(text)
}

// PromptEcho renders "you: <prompt>" with a blue prompt word.
func PromptEcho(color bool, prompt string, width int) []string {
	head := stHead(color).Render("you: ")
	out := []string{}
	for i, l := range wrap(prompt, clampWidth(width)-len("you: ")) {
		if i == 0 {
			out = append(out, head+l)
		} else {
			out = append(out, "    "+l)
		}
	}
	return out
}
