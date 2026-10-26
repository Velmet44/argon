// Package execx runs approved commands with live streaming output,
// per-command timeout, cwd_state support, and interactive-command denies.
package execx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

var interactiveDeny = []string{
	"vim", "vi", "nvim", "emacs", "nano", "code", "notepad",
	"ssh", "scp", "sftp", "ftp", "telnet",
	"less", "more", "most", "top", "htop", "btop", "watch",
	"passwd", "su", "tail",
}

// EncodePowerShell returns base64(UTF-16LE(cmd)) for -EncodedCommand.
// Avoids all $/`/quote interpolation when passing through -Command.
func EncodePowerShell(cmd string) string {
	u := utf16.Encode([]rune(cmd))
	b := make([]byte, 0, len(u)*2)
	for _, v := range u {
		b = append(b, byte(v), byte(v>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

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
		c = exec.CommandContext(ctx, bin, "-NoProfile", "-NonInteractive", "-EncodedCommand", EncodePowerShell("$ProgressPreference='SilentlyContinue'; "+cmd))
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
