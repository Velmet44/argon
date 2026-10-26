// Package execx runs approved commands with live streaming output,
// per-command timeout, cwd_state support, and interactive-command denies.
package execx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var interactiveDeny = []string{"vim", "nano", "ssh", "less", "more", "top", "passwd", "su ", "telnet"}

func IsInteractive(cmd string) bool {
	l := strings.ToLower(strings.TrimSpace(cmd))
	for _, b := range interactiveDeny {
		if l == b || strings.HasPrefix(l, b+" ") || strings.HasPrefix(l, b+"\t") {
			return true
		}
	}
	return false
}

// IsPureCD reports `cd / set-location / pushd / popd` lines, which the agent
// loop handles locally (updates cwd_state, never execs).
func IsPureCD(cmd string) bool {
	l := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range []string{"cd ", "cd\\", "set-location ", "pushd ", "popd"} {
		if strings.HasPrefix(l, p) || l == "cd" || l == "popd" {
			return true
		}
	}
	return false
}

type Result struct {
	Output   string
	ExitCode int
}

// Run streams merged output to onChunk, enforces timeout, truncates to 8k
// head+tail for LLM context. dir is cwd_state.
func Run(ctx context.Context, shell, cmd, dir string, timeout time.Duration, onChunk func(string)) Result {
	if IsInteractive(cmd) {
		return Result{Output: "argon: interactive command blocked in v0.1 (vim/nano/ssh).", ExitCode: 2}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var c *exec.Cmd
	switch shell {
	case "powershell", "pwsh":
		bin := "powershell"
		if shell == "pwsh" {
			bin = "pwsh"
		}
		if _, err := exec.LookPath(bin); err != nil && runtime.GOOS == "windows" {
			bin = "powershell"
		}
		// -EncodedCommand avoids $/quote interpolation bugs; for short
		// commands -Command is fine and keeps logs readable. Use -Command here,
		// runner writes exact string to transcript first (caller does that).
		c = exec.CommandContext(ctx, bin, "-NoProfile", "-NonInteractive", "-Command", cmd)
	default:
		c = exec.CommandContext(ctx, "bash", "-lc", cmd)
	}
	if dir != "" {
		c.Dir = dir
	}
	pr, pw := newPipe()
	c.Stdout = pw
	c.Stderr = pw
	if err := c.Start(); err != nil {
		return Result{Output: "start: " + err.Error(), ExitCode: 1}
	}
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text() + "\n"
			if buf.Len() < 256*1024 {
				buf.WriteString(line)
			}
			if onChunk != nil {
				onChunk(line)
			}
		}
		close(done)
	}()
	err := c.Wait()
	pw.Close()
	<-done
	out := buf.String()
	out = truncate(out, 8000)
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			out += "\nargon: timed out."
			code = 124
		}
	}
	return Result{Output: out, ExitCode: code}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	half := n / 2
	return s[:half] + fmt.Sprintf("\n…[%d bytes truncated]…\n", len(s)-n) + s[len(s)-half:]
}
