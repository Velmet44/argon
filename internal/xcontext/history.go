package xcontext

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Velmet44/argon/internal/risk"
)

// HistoryPaths returns candidate shell history files for this machine.
func HistoryPaths() []string {
	home, _ := os.UserHomeDir()
	cands := []string{}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		cands = append(cands, filepath.Join(appdata, `Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt`))
	}
	if home != "" {
		cands = append(cands,
			filepath.Join(home, ".local/share/powershell/PSReadLine/ConsoleHost_history.txt"),
			filepath.Join(home, ".bash_history"),
			filepath.Join(home, ".zsh_history"),
		)
	}
	return cands
}

// ReadHistory returns the last limit commands, redacted. Opens read-only,
// tolerates locks and zsh extended format. Never fails hard — returns partial.
func ReadHistory(limit int) []string {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var lines []string
	for _, p := range HistoryPaths() {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 512*1024)
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			// zsh extended: ": 1700000000:0;cmd"
			if i := strings.Index(l, ";"); strings.HasPrefix(l, ":") && i > 0 {
				l = strings.TrimSpace(l[i+1:])
			}
			if len(l) > 500 {
				l = l[:500]
			}
			lines = append(lines, risk.Redact(l))
		}
		f.Close()
		if len(lines) >= limit {
			break
		}
	}
	// dedup consecutive + take tail
	out := lines[:0]
	for _, l := range lines {
		if len(out) == 0 || out[len(out)-1] != l {
			out = append(out, l)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
