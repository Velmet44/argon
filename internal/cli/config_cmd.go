package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/config"
)

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Non-interactive settings (mirror of `argon settings`)"}
	c.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Show config (secrets masked)",
			RunE: func(cmd *cobra.Command, args []string) error {
				show, _ := cmd.Flags().GetBool("show")
				mask := func(s string) string {
					if s == "" {
						return "(env/empty)"
					}
					if show {
						return s
					}
					return "***"
				}
				fmt.Printf("llm.preset=%s\nllm.model=%s\nllm.base_url=%s\nllm.api_key=%s\n",
					cfg.LLM.Preset, cfg.LLM.Model, cfg.LLM.BaseURL, mask(cfg.APIKey()))
				fmt.Printf("defaults.shell=%s max_turns=%d llm_timeout=%d exec_timeout=%d history=%v limit=%d\n",
					cfg.Defaults.Shell, cfg.Defaults.MaxTurns, cfg.Defaults.LLMTimeoutSec, cfg.Defaults.ExecTimeoutSec, cfg.Defaults.History, cfg.Defaults.HistoryLimit)
				fmt.Printf("ui.color=%v verbose=%v stream=%v\n", cfg.UI.Color, cfg.UI.Verbose, cfg.UI.Stream)
				fmt.Printf("risk.extra_deny=%d extra_destructive=%d disabled=%v\n",
					len(cfg.Risk.ExtraDeny), len(cfg.Risk.ExtraDestructive), cfg.Risk.Disabled)
				return nil
			},
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Set one key (e.g. llm.preset groq, ui.stream false)",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				k, v := strings.ToLower(args[0]), args[1]
				switch k {
				case "llm.preset":
					cfg.LLM.Preset = v
				case "llm.model":
					cfg.LLM.Model = v
				case "llm.base_url":
					cfg.LLM.BaseURL = v
				case "llm.api_key":
					cfg.LLM.APIKey = v
				case "defaults.shell":
					cfg.Defaults.Shell = v
				case "ui.stream":
					cfg.UI.Stream = v == "true" || v == "1"
				case "ui.color":
					cfg.UI.Color = v == "true" || v == "1"
				default:
					return fmt.Errorf("unknown key %q (see SPEC Sec 9)", k)
				}
				return config.Save(cfg)
			},
		},
		&cobra.Command{
			Use:   "get <key>",
			Short: "Get one key (masked unless --show)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				show, _ := cmd.Flags().GetBool("show")
				if strings.ToLower(args[0]) == "llm.api_key" && !show {
					fmt.Println("*** (use --show to reveal)")
					return nil
				}
				fmt.Println("(see `argon config list --show`)")
				return nil
			},
		},
	)
	c.PersistentFlags().Bool("show", false, "reveal secrets")
	return c
}
