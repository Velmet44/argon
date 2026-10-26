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
	"github.com/Velmet44/argon/internal/tui"
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
	width := tui.TermWidth()
	fmt.Printf("%s %s\n", dim("◈ argon"), dim(fmt.Sprintf("· %s/%s", ci.OS, shell)+fmt.Sprintf("  +history(%d)", len(hist))))
	blank()
	printBlock(tui.PromptEcho(cfg.UI.Color, prompt, width))
	blank()

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

	lsStr := strings.Join(ci.LsTop, ", ")
	safePrompt := risk.Redact(prompt)
	var steps []llm.Step
	pendingVerify := false
	enforceOnce := false
	userCtx := llm.BuildUserMessage(shell, ci.OS, cwdState, lsStr, safePrompt, hist, steps, 1, maxTurns, "", 0, false)

	sessionCap := 0
	for turn := 1; turn <= maxTurns; turn++ {
		t0 := time.Now()
		raw, err := llmTurn(cmd.Context(), baseURL, apiKey, model, llm.SystemPrompt, llmTimeout, userCtx, nil)
		llmDur := time.Since(t0)
		if err != nil {
			lines := llmHint(err)
			if cfg.UI.Verbose {
				lines = append(lines, "detail: "+err.Error())
			}
			printErrBox(fmt.Sprintf("llm request failed · turn %d", turn), lines, width)
			_ = appendHistory("oneshot", prompt, nil, 1)
			return &SilentError{Code: 1}
		}
		if cfg.UI.Verbose {
			fmt.Println(dim(fmt.Sprintf("[turn %d · llm %s · raw %d bytes · model %s]", turn, llmDur.Round(time.Millisecond), len(raw), model)))
		}
		reply, err := llm.ParseReply(raw)
		if err != nil {
			// one repair retry: same system, assistant echoes the bad reply
			// (truncated), user carries the concrete parser error
			bad := raw
			if len(bad) > 600 {
				bad = bad[:600]
			}
			raw2, err2 := llmTurn(cmd.Context(), baseURL, apiKey, model, llm.SystemPrompt, llmTimeout,
				llm.RepairPromptFor(err.Error()), []llm.Msg{{Role: "assistant", Content: bad}})
			if err2 != nil {
				lines := []string{"The model replied with non-JSON.", "Retry — usually succeeds on the next run."}
				if cfg.UI.Verbose {
					lines = append(lines, "detail: "+err.Error())
				}
				printErrBox("could not parse model JSON", lines, width)
				_ = appendHistory("oneshot", prompt, nil, 1)
				return &SilentError{Code: 1}
			}
			reply, err = llm.ParseReply(raw2)
			if err != nil {
				lines := []string{"The model replied with non-JSON twice.", "Retry, or switch model in `argon settings`."}
				if cfg.UI.Verbose {
					lines = append(lines, "detail: "+err.Error())
				}
				printErrBox("could not parse model JSON", lines, width)
				_ = appendHistory("oneshot", prompt, nil, 1)
				return &SilentError{Code: 1}
			}
		}
		printThoughtBox(turn, llmDur, reply.Thought, width)
		blank()
		if reply.Command == nil || strings.TrimSpace(*reply.Command) == "" {
			if strings.TrimSpace(reply.Message) != "" {
				printAnswer(reply.Message, width)
			}
			_ = appendHistory("oneshot", prompt, nil, 0)
			return nil
		}
		doneFirst := reply.Done
		command := strings.TrimSpace(*reply.Command)
		// pure cd → local state update, no exec
		if execx.IsPureCD(command) {
			cwdState = resolveCD(cwdState, command)
			steps = append(steps, llm.Step{Cmd: command, Exit: 0, Digest: "cwd now " + cwdState, Note: reply.Message})
			userCtx = llm.BuildUserMessage(shell, ci.OS, cwdState, lsStr, safePrompt, hist, steps, turn+1, maxTurns, "", 0, pendingVerify)
			printCommand(command, "cd", width)
			fmt.Println(dim("cwd → " + cwdState))
			blank()
			if doneFirst {
				if strings.TrimSpace(reply.Message) != "" {
					printAnswer(reply.Message, width)
				}
				_ = appendHistory("oneshot", prompt, []string{command}, 0)
				return nil
			}
			continue
		}
		disabled := map[string]bool{}
		for _, id := range cfg.Risk.Disabled {
			disabled[id] = true
		}
		tier, why := risk.Classify(command, cfg.Risk.ExtraDeny, cfg.Risk.ExtraDestructive, disabled)
		printCommand(command, string(tier), width)
		if cfg.UI.Verbose {
			fmt.Println(dim("policy: " + why))
		}
		blank()
		if !reply.Run {
			// explicit model signal: display the plan, never execute it
			fmt.Println(dim("(model asked not to run — displaying only)"))
			blank()
			if strings.TrimSpace(reply.Message) != "" {
				printAnswer(reply.Message, width)
			}
			_ = appendHistory("oneshot", prompt, []string{command}, 0)
			return nil
		}
		switch tier {
		case risk.TierDeny:
			printErrBox("blocked", []string{why, "If this is wrong, allowlist an exception in `argon settings` → Safety."}, width)
			_ = appendHistory("oneshot", prompt, []string{command}, 2)
			return &SilentError{Code: 2}
		case risk.TierDestr:
			if !force || !confirmYES() {
				printErrBox("destructive refused", []string{"Needs --force plus typing YES.", why}, width)
				_ = appendHistory("oneshot", prompt, []string{command}, 2)
				return &SilentError{Code: 2}
			}
		case risk.TierWrite:
			if !dryRun && !yes && !confirmEnter() {
				fmt.Println(dim("(aborted — rerun with --yes to skip prompts, or answer y/Enter to run.)"))
				_ = appendHistory("oneshot", prompt, []string{command}, 1)
				return &SilentError{Code: 1}
			}
		}
		if dryRun {
			fmt.Println(dim("⊘ dry-run — not executed"))
			blank()
			_ = appendHistory("oneshot", prompt, []string{command}, 0)
			return nil
		}
		t0exec := time.Now()
		ew := newEdgeWriter(cfg.UI.Color, width)
		res := execx.Run(cmd.Context(), shell, command, cwdState, execTimeout, func(chunk string) {
			ew.Write(chunk)
		})
		ew.Flush()
		fmt.Println(dim(fmt.Sprintf("(exit %d · %s)", res.ExitCode, time.Since(t0exec).Round(time.Millisecond))))
		blank()
		redacted := risk.Redact(res.Output)
		steps = append(steps, llm.Step{Cmd: command, Exit: res.ExitCode, Digest: llm.DigestOutput(redacted), Note: reply.Message})
		pendingVerify = tier == risk.TierWrite || tier == risk.TierDestr
		enforceOnce = true
		sessionCap += len(res.Output)
		if sessionCap > 80*1024 {
			res.Output = res.Output[:80*1024] + "\n…[session cap]…"
		}
		userCtx = llm.BuildUserMessage(shell, ci.OS, cwdState, lsStr, safePrompt, hist, steps, turn+1, maxTurns, res.Output, res.ExitCode, pendingVerify)
		if doneFirst && !(pendingVerify && enforceOnce && turn < maxTurns) {
			if strings.TrimSpace(reply.Message) != "" {
				printAnswer(reply.Message, width)
			}
			_ = appendHistory("oneshot", prompt, []string{command}, res.ExitCode)
			return nil
		}
		if doneFirst {
			// engine backstop for rule 7: one enforced verification turn even
			// though the model said done — the next message demands it
			enforceOnce = false
			fmt.Println(dim("(verification required — confirming the write first)"))
			blank()
		}
		if turn == maxTurns {
			fmt.Println(dim("(max-turns reached)"))
			_ = appendHistory("oneshot", prompt, []string{command}, res.ExitCode)
			return nil
		}
	}
	return nil
}

func llmTurn(ctx context.Context, baseURL, key, model, system string, timeout time.Duration, user string, hist []llm.Msg) (string, error) {
	// spec: one backoff retry (2s) on 429/5xx, then surface the error
	raw, err := llmTurnOnce(ctx, baseURL, key, model, system, timeout, user, hist)
	if err != nil && isRateOrServerErr(err) {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
		if ctx.Err() == nil {
			return llmTurnOnce(ctx, baseURL, key, model, system, timeout, user, hist)
		}
	}
	return raw, err
}

func isRateOrServerErr(err error) bool {
	msg := err.Error()
	for _, s := range []string{" 429", " 500", " 502", " 503", "rate_limit", "Rate limit", "rate_limit_exceeded"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func llmTurnOnce(ctx context.Context, baseURL, key, model, system string, timeout time.Duration, user string, hist []llm.Msg) (string, error) {
	if !cfg.UI.Stream {
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return llm.CompleteOnce(c, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, History: hist, User: user, Timeout: timeout})
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan llm.Chunk, 64)
	go llm.StreamChat(c, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, History: hist, User: user}, ch)
	var sb strings.Builder
	streamErr := error(nil)
	stopSpin := make(chan struct{})
	spinDone := make(chan struct{})
	spinning := !cfg.UI.Verbose && isTTY()
	if spinning {
		go thinkingSpinner(cfg.UI.Color, stopSpin, spinDone)
	} else if !cfg.UI.Verbose {
		fmt.Println(dim("◈ Thinking…"))
	}
	for ck := range ch {
		if ck.Err != nil {
			streamErr = ck.Err
			break
		}
		sb.WriteString(ck.Text)
		if ck.Done {
			break
		}
	}
	if spinning {
		close(stopSpin)
		<-spinDone
	} else if !cfg.UI.Verbose {
		// static line already printed; nothing to clear.
	} else {
		fmt.Println()
	}
	if streamErr != nil {
		// SSE failed: single non-stream retry, surface combined error if it also fails
		c2, cancel2 := context.WithTimeout(ctx, timeout)
		defer cancel2()
		raw, err := llm.CompleteOnce(c2, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, History: hist, User: user, Timeout: timeout})
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
		return llm.CompleteOnce(c2, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: system, History: hist, User: user, Timeout: timeout})
	}
	return sb.String(), nil
}

func confirmEnter() bool {
	if !isTTY() {
		return false
	}
	q := "? run? [y/Enter=yes · n=no] "
	if cfg.UI.Color {
		q = "\x1b[1;33m" + q + "\x1b[0m"
	}
	fmt.Print(q)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "" || s == "y" || s == "yes"
}

func confirmYES() bool {
	if !isTTY() {
		return false
	}
	q := "! destructive — type YES + Enter: "
	if cfg.UI.Color {
		q = "\x1b[1;31m" + q + "\x1b[0m"
	}
	fmt.Print(q)
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

// SilentError carries a process exit code for failures that are already
// rendered as boxes (main prints nothing further, cobra stays silent).
type SilentError struct{ Code int }

func (e *SilentError) Error() string { return fmt.Sprintf("exit %d", e.Code) }
