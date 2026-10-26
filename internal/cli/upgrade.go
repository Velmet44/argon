package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/update"
)

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Self-update (curl installs only; winget/apt refuse with guidance)",
		RunE: func(cmd *cobra.Command, args []string) error {
			src := update.InstallSource()
			if src != "curl" && src != "go-install" && src != "unknown" {
				return fmt.Errorf("managed install (%s): use winget upgrade / apt upgrade instead — argon will not self-overwrite", src)
			}
			fmt.Println("upgrade: checking GitHub Releases… (wired in release workflow)")
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	c := &cobra.Command{Use: "version", Short: "Print version"}
	c.Run = func(cmd *cobra.Command, args []string) {
		src, _ := cmd.Flags().GetBool("source")
		if src {
			fmt.Println(update.InstallSource())
			return
		}
		fmt.Println("argon v0.0.1-scaffold (" + update.InstallSource() + ")")
	}
	c.Flags().Bool("source", false, "print install source only")
	return c
}
