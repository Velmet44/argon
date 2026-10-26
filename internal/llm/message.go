package llm

import (
	"fmt"
	"strings"
)

// Step is one finished agent turn, recorded for the STEPS ledger.
// Digest and Note are generated LOCALLY (never by the model): digest
// summarizes the output shape, note is the model's own prior message.
type Step struct {
	Cmd    string
	Exit   int
	Digest string
	Note   string
}

// DigestOutput summarizes command output shape: "N lines; first: ...".
// Only the latest output ever travels in full; older steps travel as digests.
func DigestOutput(out string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	lines := strings.Split(out, "\n")
	// drop trailing empties for the count, keep content lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	first := ""
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			first = t
			break
		}
	}
	if len(first) > 80 {
		first = first[:80]
	}
	return fmt.Sprintf("%d lines; first: %q", len(lines), first)
}

const maxOutputChars = 4000

// TruncateOutput caps the latest output (head 2500 + tail 1500) and strips
// any literal OUTPUT>>> delimiter so outputs can't break the message frame.
func TruncateOutput(out string) (string, bool) {
	out = strings.ReplaceAll(out, "OUTPUT>>>", "OUTPUT>>")
	if len(out) <= maxOutputChars {
		return out, false
	}
	const head = 2500
	tail := maxOutputChars - head
	omitted := len(out) - maxOutputChars
	return out[:head] + fmt.Sprintf("\n[... %d chars omitted ...]\n", omitted) + out[len(out)-tail:], true
}

// BuildUserMessage assembles one self-contained user message per turn.
// The original task is repeated verbatim every turn (top and bottom) so the
// model never loses it; older steps travel as local digests, never raw.
// mustVerify adds an explicit verification order when the previous turn
// modified the system (write/delete without a confirming read yet).
func BuildUserMessage(shell, os, cwd, ls, task string, shellHist []string, steps []Step, stepN, maxTurns int, out string, outExit int, mustVerify bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CONTEXT: shell=%s os=%s cwd=%s step=%d/%d\n", shell, os, cwd, stepN, maxTurns)
	if ls != "" {
		b.WriteString("ls: " + ls + "\n")
	}
	if len(shellHist) > 0 {
		b.WriteString("recent shell history:\n" + strings.Join(shellHist, "\n") + "\n")
	}
	b.WriteString("TASK: " + task + "\n")
	b.WriteString("STEPS (yours, oldest first):\n")
	if len(steps) == 0 {
		b.WriteString("none yet\n")
	} else {
		for i, s := range steps {
			fmt.Fprintf(&b, "%d. %s -> exit %d, %s\n", i+1, s.Cmd, s.Exit, s.Digest)
			if s.Note != "" {
				fmt.Fprintf(&b, "   note: %s\n", s.Note)
			}
		}
	}
	if stepN == 1 {
		b.WriteString("LATEST OUTPUT: none (first step)\n")
	} else {
		trunc, wasTrunc := TruncateOutput(out)
		tag := fmt.Sprintf("LATEST OUTPUT (step %d, exit %d", stepN-1, outExit)
		if wasTrunc {
			tag += ", truncated"
		}
		b.WriteString(tag + "):\n<<<OUTPUT\n")
		if strings.TrimSpace(trunc) == "" {
			b.WriteString("(no output)\n")
		} else {
			b.WriteString(trunc)
			if !strings.HasSuffix(trunc, "\n") {
				b.WriteString("\n")
			}
		}
		b.WriteString("OUTPUT>>>\n")
	}
	fmt.Fprintf(&b, "NEXT: Reply with one JSON object. Original task again: %q. Does LATEST OUTPUT already answer it? Yes -> done:true with the values. No -> the single next command.", task)
	if mustVerify {
		b.WriteString(" Your last command modified the system — verify it with a read-only command now (done:false).")
	}
	if stepN >= maxTurns {
		b.WriteString(" FINAL STEP: set done:true now.")
	}
	return b.String()
}
