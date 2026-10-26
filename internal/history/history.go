// Package history owns the local transcript log (~/.argon/history.jsonl):
// one line per finished task with redacted, truncated outputs.
//
// Nothing here leaves the machine except as prompt context (see the
// PREVIOUS TASKS section of llm.BuildUserMessage). Secrets are redacted
// with the same rules as prompt context, the file is 0600, and
// `argon history --clear` wipes it.
package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Velmet44/argon/internal/config"
	"github.com/Velmet44/argon/internal/llm"
	"github.com/Velmet44/argon/internal/risk"
)

// Entry is one finished task. Older files lack Outputs (tolerated on read).
type Entry struct {
	TS       time.Time `json:"ts"`
	Mode     string    `json:"mode"`
	Prompt   string    `json:"prompt"`
	Commands []string  `json:"commands"`
	Outputs  []string  `json:"outputs,omitempty"`
	Exit     int       `json:"exit"`
}

const defaultPerCmd = 800
const defaultKeepMax = 200

func path() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "history.jsonl"), nil
}

// Append stores one finished task. Each output is redacted and truncated to
// perCmd chars (<=0 selects the default); the file is pruned to keepMax lines.
func Append(mode, prompt string, cmds, outs []string, exit int, perCmd, keepMax int) error {
	if perCmd <= 0 {
		perCmd = defaultPerCmd
	}
	if keepMax <= 0 {
		keepMax = defaultKeepMax
	}
	p, err := path()
	if err != nil {
		return nil // history must never break a session
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil
	}
	clean := make([]string, 0, len(outs))
	for _, o := range outs {
		o = risk.Redact(o)
		if len(o) > perCmd {
			o = o[:perCmd]
		}
		clean = append(clean, o)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	e := Entry{TS: time.Now().UTC(), Mode: mode, Prompt: prompt, Commands: cmds, Outputs: clean, Exit: exit}
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
	f.Close()
	return prune(p, keepMax)
}

func prune(p string, keepMax int) error {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) <= keepMax || (len(lines) == 1 && lines[0] == "") {
		return nil
	}
	tail := lines[len(lines)-keepMax:]
	return os.WriteFile(p, []byte(strings.Join(tail, "\n")+"\n"), 0o600)
}

// LoadRecent returns up to n finished tasks, oldest first. Missing files,
// blank lines and corrupt/legacy lines are skipped, never fatal.
func LoadRecent(n int) ([]Entry, error) {
	if n <= 0 {
		return nil, nil
	}
	p, err := path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256*1024), 2*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// RecentTasks maps the file tail to llm-ready previous tasks, combining each
// entry's commands/outputs and capping the combined output at outCap chars.
func RecentTasks(n, outCap int) ([]llm.PrevTask, error) {
	ents, err := LoadRecent(n)
	if err != nil {
		return nil, err
	}
	out := make([]llm.PrevTask, 0, len(ents))
	for _, e := range ents {
		joined := strings.Join(e.Outputs, "\n---\n")
		if outCap > 0 && len(joined) > outCap {
			joined = joined[:outCap] + "…"
		}
		out = append(out, llm.PrevTask{
			Prompt: e.Prompt,
			Cmd:    strings.Join(e.Commands, " ; "),
			Out:    joined,
		})
	}
	return out, nil
}

// Clear wipes the transcript log. Returns true if a file was removed.
func Clear() (bool, error) {
	p, err := path()
	if err != nil {
		return false, err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
