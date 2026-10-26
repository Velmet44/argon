package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/Velmet44/argon/internal/tui"
)

// blank separates blocks: exactly one empty line, never more.
func blank() { fmt.Println() }

// edgeWriter prefixes streamed terminal output with the grey "│ " edge,
// buffering partial lines so chunk splits never break the rule.
type edgeWriter struct {
	prefix string
	buf    strings.Builder
	empty  bool
	n      int
}

func newEdgeWriter(color bool, width int) *edgeWriter {
	_ = width
	bar := "│ "
	if color {
		bar = "\x1b[2m│ \x1b[0m"
	}
	return &edgeWriter{prefix: bar, empty: true}
}

func (w *edgeWriter) Write(chunk string) {
	w.buf.WriteString(chunk)
	s := w.buf.String()
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		w.emitLine(s[:i])
		s = s[i+1:]
	}
	w.buf.Reset()
	w.buf.WriteString(s)
}

func (w *edgeWriter) emitLine(line string) {
	line = strings.TrimRight(line, "\r")
	fmt.Println(w.prefix + line)
	w.empty = false
	w.n++
}

func (w *edgeWriter) Flush() {
	if rest := w.buf.String(); rest != "" {
		w.emitLine(rest)
		w.buf.Reset()
	}
	if w.empty {
		fmt.Println(dim("(no output)"))
	}
}

// thinkingSpinner prints a \r-updated "◈ Thinking… Ns" line on a FIXED width
// so overwrites can never leave residue; end with a clean newline.
func thinkingSpinner(color bool, stop <-chan struct{}, done chan<- struct{}) {
	start := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	defer close(done)
	line := func() string {
		s := fmt.Sprintf("◈ Thinking… %5.1fs", time.Since(start).Seconds())
		if color {
			return "\x1b[2;36m" + s + "\x1b[0m"
		}
		return s
	}
	fmt.Print(line())
	for {
		select {
		case <-stop:
			fmt.Println()
			return
		case <-tick.C:
			fmt.Print("\r" + line())
		}
	}
}

// printBlock prints pre-rendered lines as one block (no trailing blank;
// callers own spacing via blank()).
func printBlock(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

func printThoughtBox(n int, elapsed time.Duration, thought string, width int) {
	printBlock(tui.ThoughtBoxLines(cfg.UI.Color, n, elapsed, thought, width))
}

func printCommand(cmd, tier string, width int) {
	printBlock(tui.CommandLines(cfg.UI.Color, cmd, tier, width))
}

func printAnswer(msg string, width int) {
	printBlock(tui.AnswerLines(cfg.UI.Color, msg, width))
}

func printErrBox(title string, lines []string, width int) {
	printBlock(tui.ErrBoxLines(cfg.UI.Color, title, lines, width))
}

// llmHint maps transport failures to self-service hints.
func llmHint(err error) []string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, " 401") || strings.Contains(msg, "401:"):
		return []string{"API key rejected.", "Paste a fresh key: `argon settings` (get one at https://console.groq.com/keys)."}
	case strings.Contains(msg, " 404") || strings.Contains(msg, "404:"):
		return []string{"Model id not served for this key.", "Pick a current id in `argon settings` → provider (selector lists verified ids)."}
	case strings.Contains(msg, " 410") || strings.Contains(msg, "410:"):
		return []string{"That model reached end-of-life.", "Pick a current id in `argon settings` → provider."}
	case strings.Contains(msg, " 429") || strings.Contains(msg, "429:"):
		return []string{"Rate limited (free-tier quota).", "Wait a minute and retry, or lower --max-turns."}
	case strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timed out"):
		return []string{"Request timed out — free-tier queue is busy.", "Retry; streaming usually returns first tokens faster."}
	default:
		return []string{msg}
	}
}
