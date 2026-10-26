package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// runREPL is the fullscreen BubbleTea app. v0.0 scaffold prints guidance;
// full viewport lands next iteration (SPEC 2.2/6.1 states already specced).
func runREPL(cmd *cobra.Command) error {
	if !isTTY() {
		return fmt.Errorf("argon REPL needs a TTY — use: argon \"your task\" [--dry-run]")
	}
	fmt.Println("argon REPL (scaffold) — fullscreen TUI lands next.")
	fmt.Println("Use: argon \"list big files\" --dry-run | argon settings | argon config list")
	return nil
}
