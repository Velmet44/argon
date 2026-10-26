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
		return fmt.Errorf("no API key — get a free Groq key at %s, then `argon settings` (or set %s)", llm.GroqKeyURL, preset.KeyEnv)
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
		t0 := time.Now()
		raw, err := llmTurn(cmd.Context(), baseURL, apiKey, model, llm.SystemPrompt, llmTimeout, userCtx)
		if err != nil {
			return fmt.Errorf("llm turn %d failed after %s: %v", turn, time.Since(t0).Round(time.Millisecond), err)
		}
		if cfg.UI.Verbose {
			fmt.Println(dim(fmt.Sprintf("[turn %d llm %s raw %d bytes]", turn, time.Since(t0).Round(time.Millisecond), len(raw))))
			fmt.Println(dim("raw: " + truncateRaw(raw, 2000)))
		}
		reply, err := llm.ParseReply(raw)
		if err != nil {
			// one repair retry: system stays, user = repair instruction + bad output
			raw2, err2 := llmTurn(cmd.Context(), baseURL, apiKey, model, llm.SystemPrompt, llmTimeout, llm.RepairPrompt+"\n\nBAD OUTPUT:\n"+raw)
			if err2 != nil {
				return fmt.Errorf("could not parse model JSON (use --verbose): %v", err)
			}
			if cfg.UI.Verbose {
				fmt.Println(dim("repair raw: " + truncateRaw(raw2, 2000)))
			}
			reply, err = llm.ParseReply(raw2)
			if err != nil {
				return fmt.Errorf("could not parse model JSON (use --verbose): %v", err)
			}
		}
		if reply.Thought != "" {
			fmt.Println(dim("◈ "+reply.Thought))
		}
		if reply.Command == nil || strings.TrimSpace(*reply.Command) == "" {
			fmt.Println(reply.Message)
			return appendHistory("oneshot", prompt, nil, 0)
		}
		if reply.Done {
			fmt.Println(dim("(final step)"))
		}
		doneFirst := reply.Done
		command := strings.TrimSpace(*reply.Command)
		// pure cd → local state update, no exec
		if execx.IsPureCD(command) {
			cwdState = resolveCD(cwdState, command)
			userCtx = fmt.Sprintf("(cd → %s)\n%s", cwdState, userCtx)
			fmt.Println(boldYellow("$ " + command + "  (cwd → " + cwdState + ")"))
			if doneFirst {
				fmt.Println(reply.Message)
				return appendHistory("oneshot", prompt, []string{command}, 0)
			}
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
			if !dryRun && !yes && !confirmEnter("run? [Enter/y=yes, n=no] ") {
				fmt.Println("aborted — rerun with --yes to skip prompts, or answer y/Enter to run.")
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
		if doneFirst {
			fmt.Println(reply.Message)
			return appendHistory("oneshot", prompt, []string{command}, res.ExitCode)
		}
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
	streamErr := error(nil)
	showRaw := cfg.UI.Verbose && cfg.UI.Color && isTTY()
	if !cfg.UI.Verbose {
		fmt.Println(dim("thinking…"))
	}
	for ck := range ch {
		if ck.Err != nil {
			streamErr = ck.Err
			break
		}
		sb.WriteString(ck.Text)
		if showRaw {
			fmt.Print(dim(ck.Text))
		}
		if ck.Done {
			break
		}
	}
	if !cfg.UI.Verbose {
		// thinking… already ended with newline; nothing to clear.
	} else {
		fmt.Println()
	}
	if streamErr != nil {
		// SSE failed: single non-stream retry, surface combined error if it also fails
		c2, cancel2 := context.WithTimeout(ctx, timeout)
		defer cancel2()
		raw, err := llm.CompleteOnce(c2, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, User: user, Timeout: timeout})
		if err != nil {
			return "", fmt.Errorf("stream failed (%v) + retry failed (%v)", streamErr, err)
		}
		if cfg.UI.Verbose {
			fmt.Println(dim(fmt.Sprintf("[stream fallback to non-stream after: %v]", streamErr)))
		}
		return raw, nil
	}
	if sb.Len() == 0 {
		// empty stream (provider sent no deltas): one non-stream retry
		c2, cancel2 := context.WithTimeout(ctx, timeout)
		defer cancel2()
		return llm.CompleteOnce(c2, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, User: user, Timeout: timeout})
	}
	return sb.String(), nil
}

func truncateRaw(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func confirmEnter(msg string) bool {
	if !isTTY() {
		return false
	}
	fmt.Print(msg)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "" || s == "y" || s == "yes"
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
