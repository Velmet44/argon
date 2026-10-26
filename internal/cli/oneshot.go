package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/execx"
	"github.com/Velmet44/argon/internal/llm"
	"github.com/Velmet44/argon/internal/risk"
	"github.com/Velmet44/argon/internal/xcontext"
)

func runOneShot(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return runREPL(cmd)
	}
	prompt := strings.Join(args, " ")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")

	shell := cfg.Defaults.Shell
	if shell == "" || shell == "auto" {
		shell = xcontext.DetectShell()
	}
	if shell == "cmd" || shell == "sh" {
		return fmt.Errorf("shell %q not supported in v0.1 — use powershell/pwsh on Windows, bash/zsh on Linux", shell)
	}
	ci := xcontext.Collect()
	useHist := cfg.Defaults.History
	if f := cmd.Flags(); f.Changed("history") {
		useHist, _ = f.GetBool("history")
	}
	limit := cfg.Defaults.HistoryLimit
	if limit <= 0 {
		limit = 50
	}
	var hist []string
	if useHist {
		hist = xcontext.ReadHistory(limit)
	}
	fmt.Printf("◈ argon (%s/%s) %s\n", ci.OS, shell, dim(fmt.Sprintf("+history(%d)", len(hist))))

	preset := llm.Lookup(cfg.LLM.Preset)
	baseURL := cfg.LLM.BaseURL
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	model := cfg.LLM.Model
	if model == "" {
		model = preset.Model
	}
	apiKey := cfg.APIKey()
	if apiKey == "" {
		return fmt.Errorf("no API key — set %s (or ARGON_API_KEY) or run `argon settings`", preset.KeyEnv)
	}

	cwdState := ci.CWD
	maxTurns := cfg.Defaults.MaxTurns
	llmTimeout := time.Duration(cfg.Defaults.LLMTimeoutSec) * time.Second
	if llmTimeout <= 0 {
		llmTimeout = 90 * time.Second
	}
	execTimeout := time.Duration(cfg.Defaults.ExecTimeoutSec) * time.Second
	if execTimeout <= 0 {
		execTimeout = 60 * time.Second
	}

	userCtx := fmt.Sprintf("os=%s shell=%s cwd=%s\nls: %s", ci.OS, shell, cwdState, strings.Join(ci.LsTop, ", "))
	if len(hist) > 0 {
		userCtx += "\nrecent shell history:\n" + strings.Join(hist, "\n")
	}
	userCtx += "\n\ntask: " + risk.Redact(prompt)

	sessionCap := 0
	for turn := 1; turn <= maxTurns; turn++ {
		raw, err := llmTurn(cmd.Context(), baseURL, apiKey, model, llm.SystemPrompt, llmTimeout, userCtx)
		if err != nil {
			return err
		}
		reply, err := llm.ParseReply(raw)
		if err != nil {
			// one repair retry
			raw2, err2 := llmTurn(cmd.Context(), baseURL, apiKey, model, llm.RepairPrompt+"\n\n"+raw, llmTimeout, "")
			if err2 != nil {
				return fmt.Errorf("could not parse model JSON (use --verbose): %v", err)
			}
			reply, err = llm.ParseReply(raw2)
			if err != nil {
				return fmt.Errorf("could not parse model JSON (use --verbose): %v", err)
			}
		}
		if reply.Thought != "" {
			fmt.Println(dim("◈ "+reply.Thought))
		}
		if reply.Done || reply.Command == nil || *reply.Command == "" {
			fmt.Println(reply.Message)
			return appendHistory("oneshot", prompt, nil, 0)
		}
		command := strings.TrimSpace(*reply.Command)
		// pure cd → local state update, no exec
		if execx.IsPureCD(command) {
			cwdState = resolveCD(cwdState, command)
			userCtx = fmt.Sprintf("(cd → %s)\n%s", cwdState, userCtx)
			fmt.Println(boldYellow("$ " + command + "  (cwd → " + cwdState + ")"))
			continue
		}
		disabled := map[string]bool{}
		for _, id := range cfg.Risk.Disabled {
			disabled[id] = true
		}
		tier, why := risk.Classify(command, cfg.Risk.ExtraDeny, cfg.Risk.ExtraDestructive, disabled)
		fmt.Println(boldYellow("$ " + command) + "  " + dim(fmt.Sprintf("[%s]", tier)))
		if cfg.UI.Verbose {
			fmt.Println(dim("policy: " + why))
		}
		switch tier {
		case risk.TierDeny:
			fmt.Println("blocked: " + why)
			return appendHistory("oneshot", prompt, []string{command}, 2)
		case risk.TierDestr:
			if !force || !confirmYES("destructive — type YES to run: ") {
				fmt.Println("needs --force + typed YES. Aborted.")
				return appendHistory("oneshot", prompt, []string{command}, 2)
			}
		case risk.TierWrite:
			if !dryRun && !yes && !confirmEnter("run? [Enter=yes Ctrl+C=no] ") {
				return appendHistory("oneshot", prompt, []string{command}, 1)
			}
		}
		if dryRun {
			fmt.Println(dim("(dry-run, not executed)"))
			return appendHistory("oneshot", prompt, []string{command}, 0)
		}
		fmt.Println(dim("running…"))
		res := execx.Run(cmd.Context(), shell, command, cwdState, execTimeout, func(chunk string) {
			fmt.Print(chunk)
		})
		sessionCap += len(res.Output)
		if sessionCap > 80*1024 {
			res.Output = res.Output[:80*1024] + "\n…[session cap]…"
		}
		userCtx = fmt.Sprintf("command: %s\nexit: %d\noutput:\n%s", command, res.ExitCode, risk.Redact(res.Output))
		if turn == maxTurns {
			fmt.Println(dim("(max-turns reached)"))
			return appendHistory("oneshot", prompt, []string{command}, res.ExitCode)
		}
	}
	return nil
}

func llmTurn(ctx context.Context, baseURL, key, model, system string, timeout time.Duration, user string) (string, error) {
	if !cfg.UI.Stream {
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return llm.CompleteOnce(c, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, User: user, Timeout: timeout})
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan llm.Chunk, 64)
	go llm.StreamChat(c, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, User: user}, ch)
	var sb strings.Builder
	useColor := cfg.UI.Color
	for ck := range ch {
		if ck.Err != nil {
			// SSE fallback: single non-stream retry
			c2, cancel2 := context.WithTimeout(ctx, timeout)
			defer cancel2()
			return llm.CompleteOnce(c2, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, User: user, Timeout: timeout})
		}
		sb.WriteString(ck.Text)
		if useColor && !isTTY() {
			// non-TTY: no live colors
		} else if cfg.UI.Stream {
			fmt.Print(dim(ck.Text))
		}
		if ck.Done {
			break
		}
	}
	fmt.Println()
	return sb.String(), nil
}

func confirmEnter(msg string) bool {
	if !isTTY() {
		return false
	}
	fmt.Print(msg)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s) == ""
}

func confirmYES(msg string) bool {
	if !isTTY() {
		return false
	}
	fmt.Print(msg)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s) == "YES"
}

func dim(s string) string {
	if !cfg.UI.Color {
		return s
	}
	return "\x1b[2;36m" + s + "\x1b[0m"
}

func boldYellow(s string) string {
	if !cfg.UI.Color {
		return s
	}
	return "\x1b[1;33m" + s + "\x1b[0m"
}
