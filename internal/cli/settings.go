package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "settings",
		Short: "Open the full settings TUI (provider, safety, defaults, appearance)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("argon settings TUI (scaffold) — ships next iteration.")
			fmt.Println("Meanwhile: argon config list | argon config set llm.preset groq")
			return nil
		},
	}
}
