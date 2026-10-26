package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Velmet44/argon/internal/history"
)

func resolveCD(base, cmd string) string {
	s := strings.TrimSpace(cmd)
	// strip trailing ; and & chains (pure-cd lines shouldn't have them, be tolerant)
	s = strings.TrimRight(s, "; \t")
	// Prefer last quoted segment: handles Set-Location "sub dir", cd 'a b'.
	if q := lastQuoted(s); q != "" {
		return joinBase(base, q)
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return base
	}
	head := strings.ToLower(parts[0])
	switch head {
	case "cd", "chdir", "set-location", "sl", "pushd":
		target := lastNonFlag(parts[1:])
		if target == "" {
			if head == "pushd" {
				return base // v0.1: no dir stack, pushd bare = no-op
			}
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				return filepath.Clean(home)
			}
			return base
		}
		return joinBase(base, target)
	case "popd":
		return base // v0.1: no stack tracking, documented
	case "cd\\":
		return base
	}
	// "cd /d <path>" (cmd-style tolerance)
	if len(parts) >= 3 && strings.EqualFold(parts[0], "cd") && strings.EqualFold(parts[1], "/d") {
		return joinBase(base, parts[2])
	}
	return base
}

func lastQuoted(s string) string {
	// last "..." pair wins, else last '...' pair
	for _, q := range []byte{'"', '\''} {
		parts := strings.Split(s, string(q))
		if len(parts) >= 3 {
			inner := strings.TrimSpace(parts[len(parts)-2])
			if inner != "" {
				return inner
			}
		}
	}
	return ""
}

func isAbsCross(target string) bool {
	if filepath.IsAbs(target) {
		return true
	}
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\`) {
		return true
	}
	if len(target) >= 2 && target[1] == ':' {
		return true
	}
	return false
}
func lastNonFlag(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		a := strings.Trim(args[i], `"';`)
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return strings.Trim(a, `"'`)
	}
	return ""
}

func joinBase(base, target string) string {
	target = strings.Trim(target, `"'`)
	if target == "~" || strings.HasPrefix(target, "~/") || strings.HasPrefix(target, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if target == "~" {
				return filepath.Clean(home)
			}
			return filepath.Clean(filepath.Join(home, target[2:]))
		}
	}
	// cross-platform abs (Windows filepath.IsAbs misses "/" when running on Win)
	if isAbsCross(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}

// appendHistory records one finished task (all considered commands + their
// outputs) to the local transcript log. History must never break a session.
func appendHistory(mode, prompt string, cmds, outs []string, exit int) error {
	return history.Append(mode, prompt, cmds, outs, exit, cfg.History.RememberOutputChars, cfg.History.FileMax)
}
