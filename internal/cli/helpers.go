package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Velmet44/argon/internal/config"
)

func resolveCD(base, cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) < 2 {
		return base
	}
	target := strings.Trim(parts[len(parts)-1], `"'`)
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}

type histEntry struct {
	TS       time.Time `json:"ts"`
	Mode     string    `json:"mode"`
	Prompt   string    `json:"prompt"`
	Commands []string  `json:"commands"`
	Exit     int       `json:"exit"`
}

func appendHistory(mode, prompt string, cmds []string, exit int) error {
	d, err := config.Dir()
	if err != nil {
		return nil
	}
	_ = os.MkdirAll(d, 0o700)
	f, err := os.OpenFile(filepath.Join(d, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	defer f.Close()
	e := histEntry{TS: time.Now().UTC(), Mode: mode, Prompt: prompt, Commands: cmds, Exit: exit}
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
	return nil
}
