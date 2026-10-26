package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/tui"
	"github.com/Velmet44/argon/internal/xcontext"
)

func runREPL(cmd *cobra.Command) error {
	if !isTTY() {
		return fmt.Errorf("argon REPL needs a TTY — use: argon \"your task\" [--dry-run]")
	}
	shell := cfg.Defaults.Shell
	if shell == "" || shell == "auto" {
		shell = xcontext.DetectShell()
	}
	ci := xcontext.Collect()
	var hist []string
	if cfg.History.ShellHistory {
		limit := cfg.History.ShellHistoryLimit
		if limit <= 0 {
			limit = 50
		}
		hist = xcontext.ReadHistory(limit)
	}
	force, _ := cmd.Flags().GetBool("force")
	return tui.RunREPL(cfg, ci.OS, shell, ci.CWD, ci.LsTop, hist, force)
}
