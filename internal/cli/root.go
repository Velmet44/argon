package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/config"
)

var cfg config.Config

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "argon [prompt]",
		Short: "Natural-language shell — describe it in English, Argon crafts the command",
		Long: `Argon one-shot: argon "list files over 1GB" [--dry-run --history --yes --force]
Advanced REPL:  argon (no args)
Settings TUI:   argon settings`,
		Args: cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "argon: config load:", err)
			}
			cfg = c
			applyFlagOverrides(cmd)
			return nil
		},
		RunE: runOneShot,
	}
	f := root.PersistentFlags()
	f.String("provider", "", "provider preset (default from config)")
	f.String("model", "", "model override")
	f.String("shell", "", "shell override: auto|powershell|pwsh|bash|zsh")
	f.Int("max-turns", 0, "max agent turns (default 8, max 20)")
	f.Int("llm-timeout", 0, "seconds per LLM turn (default 90)")
	f.Int("exec-timeout", 0, "seconds per command (default 60)")
	f.Bool("history", false, "include shell history file in context")
	f.Int("history-limit", 0, "last N history entries (default 50)")
	f.Bool("yes", false, "auto-confirm WRITE (never DESTRUCTIVE)")
	f.Bool("force", false, "required for DESTRUCTIVE (with typed YES)")
	f.Bool("dry-run", false, "plan only, never execute")
	f.Bool("verbose", false, "show JSON + timings")
	f.Bool("no-color", false, "plain output")
	f.Bool("stream", true, "stream LLM tokens (default true)")

	root.AddCommand(newSettingsCmd(), newConfigCmd(), newHistoryCmd(), newUpgradeCmd(), newVersionCmd())
	return root
}

func applyFlagOverrides(cmd *cobra.Command) {
	f := cmd.Flags()
	if v, _ := f.GetString("provider"); v != "" {
		cfg.LLM.Preset = v
	}
	if v, _ := f.GetString("model"); v != "" {
		cfg.LLM.Model = v
	}
	if v, _ := f.GetString("shell"); v != "" {
		cfg.Defaults.Shell = v
	}
	if v, _ := f.GetInt("max-turns"); v != 0 {
		cfg.Defaults.MaxTurns = v
	}
	if v, _ := f.GetInt("llm-timeout"); v != 0 {
		cfg.Defaults.LLMTimeoutSec = v
	}
	if v, _ := f.GetInt("exec-timeout"); v != 0 {
		cfg.Defaults.ExecTimeoutSec = v
	}
	if v, _ := f.GetInt("history-limit"); v != 0 {
		cfg.Defaults.HistoryLimit = v
	}
	if changed(f, "history") {
		v, _ := f.GetBool("history")
		cfg.Defaults.History = v
	}
	if changed(f, "verbose") {
		v, _ := f.GetBool("verbose")
		cfg.UI.Verbose = v
	}
	if changed(f, "no-color") {
		v, _ := f.GetBool("no-color")
		if v {
			cfg.UI.Color = false
		}
	}
	if changed(f, "stream") {
		v, _ := f.GetBool("stream")
		cfg.UI.Stream = v
	}
}

func changed(f interface {
	Changed(string) bool
}, n string) bool {
	return f.Changed(n)
}
