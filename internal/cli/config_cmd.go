package cli

import (
	"fmt"
	"os"
	"strconv"
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
				fmt.Printf("defaults.shell=%s max_turns=%d llm_timeout_sec=%d exec_timeout_sec=%d confirm_write=%v\n",
					cfg.Defaults.Shell, cfg.Defaults.MaxTurns, cfg.Defaults.LLMTimeoutSec, cfg.Defaults.ExecTimeoutSec,
					cfg.Defaults.ConfirmWrite)
				fmt.Printf("history.remember_turns=%v remember_limit=%d remember_output_chars=%d shell_history=%v shell_history_limit=%d file_max=%d\n",
					cfg.History.RememberTurns, cfg.History.RememberLimit, cfg.History.RememberOutputChars,
					cfg.History.ShellHistory, cfg.History.ShellHistoryLimit, cfg.History.FileMax)
				fmt.Printf("ui.color=%v ui.verbose=%v ui.stream=%v\n", cfg.UI.Color, cfg.UI.Verbose, cfg.UI.Stream)
				fmt.Printf("risk.extra_deny=%s\nrisk.extra_destructive=%s\nrisk.disabled=%s\n",
					strings.Join(cfg.Risk.ExtraDeny, ","), strings.Join(cfg.Risk.ExtraDestructive, ","), strings.Join(cfg.Risk.Disabled, ","))
				return nil
			},
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Set one key — same fields as `argon settings`",
			Long: "Keys: llm.preset|model|base_url|api_key, defaults.shell|max_turns|llm_timeout_sec|exec_timeout_sec|confirm_write, history.remember_turns|remember_limit|remember_output_chars|shell_history|shell_history_limit|file_max, ui.color|verbose|stream, risk.extra_deny|extra_destructive|disabled (comma-sep)",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				k, v := strings.ToLower(args[0]), args[1]
				atoi := func(min, max int) (int, error) {
					n, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil || n < min || n > max {
						return 0, fmt.Errorf("%s must be %d–%d", k, min, max)
					}
					return n, nil
				}
				boolean := func() bool { return v == "true" || v == "1" || strings.ToLower(v) == "on" }
				splitv := func() []string {
					var out []string
					for _, p := range strings.Split(v, ",") {
						if t := strings.TrimSpace(p); t != "" {
							out = append(out, t)
						}
					}
					return out
				}
				var err error
				switch k {
				case "llm.preset":
					cfg.LLM.Preset = strings.ToLower(v)
				case "llm.model":
					cfg.LLM.Model = v
				case "llm.base_url":
					cfg.LLM.BaseURL = v
				case "llm.api_key":
					cfg.LLM.APIKey = v
				case "defaults.shell":
					cfg.Defaults.Shell = strings.ToLower(v)
				case "defaults.max_turns":
					cfg.Defaults.MaxTurns, err = atoi(1, 20)
				case "defaults.llm_timeout_sec":
					cfg.Defaults.LLMTimeoutSec, err = atoi(10, 300)
				case "defaults.exec_timeout_sec":
					cfg.Defaults.ExecTimeoutSec, err = atoi(5, 600)
				case "defaults.history":
					cfg.History.ShellHistory = boolean()
				case "defaults.history_limit":
					cfg.History.ShellHistoryLimit, err = atoi(1, 200)
				case "defaults.confirm_write":
					cfg.Defaults.ConfirmWrite = boolean()
				case "history.remember_turns":
					cfg.History.RememberTurns = boolean()
				case "history.remember_limit":
					cfg.History.RememberLimit, err = atoi(1, 20)
				case "history.remember_output_chars":
					cfg.History.RememberOutputChars, err = atoi(100, 8000)
				case "history.shell_history":
					cfg.History.ShellHistory = boolean()
				case "history.shell_history_limit":
					cfg.History.ShellHistoryLimit, err = atoi(1, 200)
				case "history.file_max":
					cfg.History.FileMax, err = atoi(10, 5000)
				case "ui.color":
					cfg.UI.Color = boolean()
				case "ui.verbose":
					cfg.UI.Verbose = boolean()
				case "ui.stream":
					cfg.UI.Stream = boolean()
				case "risk.extra_deny":
					cfg.Risk.ExtraDeny = splitv()
				case "risk.extra_destructive":
					cfg.Risk.ExtraDestructive = splitv()
				case "risk.disabled":
					cfg.Risk.Disabled = splitv()
				default:
					return fmt.Errorf("unknown key %q (see `argon config list`)", k)
				}
				if err != nil {
					return err
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
				k := strings.ToLower(args[0])
				if k == "llm.api_key" && !show {
					fmt.Println("*** (use --show to reveal)")
					return nil
				}
				switch k {
				case "llm.preset":
					fmt.Println(cfg.LLM.Preset)
				case "llm.model":
					fmt.Println(cfg.LLM.Model)
				case "llm.base_url":
					fmt.Println(cfg.LLM.BaseURL)
				case "llm.api_key":
					fmt.Println(cfg.APIKey())
				case "defaults.shell":
					fmt.Println(cfg.Defaults.Shell)
				case "defaults.max_turns":
					fmt.Println(cfg.Defaults.MaxTurns)
				case "defaults.llm_timeout_sec":
					fmt.Println(cfg.Defaults.LLMTimeoutSec)
				case "defaults.exec_timeout_sec":
					fmt.Println(cfg.Defaults.ExecTimeoutSec)
				case "defaults.history":
					fmt.Println(cfg.History.ShellHistory, "(legacy alias of history.shell_history)")
				case "defaults.history_limit":
					fmt.Println(cfg.History.ShellHistoryLimit, "(legacy alias of history.shell_history_limit)")
				case "defaults.confirm_write":
					fmt.Println(cfg.Defaults.ConfirmWrite)
				case "history.remember_turns":
					fmt.Println(cfg.History.RememberTurns)
				case "history.remember_limit":
					fmt.Println(cfg.History.RememberLimit)
				case "history.remember_output_chars":
					fmt.Println(cfg.History.RememberOutputChars)
				case "history.shell_history":
					fmt.Println(cfg.History.ShellHistory)
				case "history.shell_history_limit":
					fmt.Println(cfg.History.ShellHistoryLimit)
				case "history.file_max":
					fmt.Println(cfg.History.FileMax)
				case "ui.color":
					fmt.Println(cfg.UI.Color)
				case "ui.verbose":
					fmt.Println(cfg.UI.Verbose)
				case "ui.stream":
					fmt.Println(cfg.UI.Stream)
				case "risk.extra_deny":
					fmt.Println(strings.Join(cfg.Risk.ExtraDeny, ","))
				case "risk.extra_destructive":
					fmt.Println(strings.Join(cfg.Risk.ExtraDestructive, ","))
				case "risk.disabled":
					fmt.Println(strings.Join(cfg.Risk.Disabled, ","))
				default:
					return fmt.Errorf("unknown key %q", k)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "export <path>",
			Short: "Copy config file to path",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return config.SaveTo(cfg, args[0])
			},
		},
		&cobra.Command{
			Use:   "import <path>",
			Short: "Load config file from path (overwrites current)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				loaded, err := config.LoadFrom(args[0])
				if err != nil {
					return err
				}
				cfg = loaded
				return config.Save(cfg)
			},
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Reset all settings to defaults",
			RunE: func(cmd *cobra.Command, args []string) error {
				yes, _ := cmd.Flags().GetBool("yes")
				if !yes {
					fmt.Fprintln(os.Stderr, "add --yes to confirm reset")
					return nil
				}
				cfg = config.Default()
				return config.Save(cfg)
			},
		},
	)
	c.PersistentFlags().Bool("show", false, "reveal secrets")
	reset := c.Commands()[len(c.Commands())-1]
	reset.Flags().Bool("yes", false, "confirm reset")
	return c
}
