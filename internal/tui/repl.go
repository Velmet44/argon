// Package tui hosts the fullscreen `argon` REPL (BubbleTea).
// States: input -> streaming -> confirming? -> running -> streaming... -> input.
// LLM tokens stream live via Program.Send; command output streams live too.
// Ctrl+C cancels the active turn, never kills the REPL (Ctrl+D or /quit exits).
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/Velmet44/argon/internal/config"
	"github.com/Velmet44/argon/internal/execx"
	"github.com/Velmet44/argon/internal/llm"
	"github.com/Velmet44/argon/internal/risk"
)

type mode int

const (
	modeInput mode = iota
	modeStreaming
	modeConfirmWrite
	modeConfirmDestr
	modeRunning
)

type chunkMsg struct{ text string }
type doneMsg struct{ raw string }
type errMsg struct{ err error }
type execLineMsg struct{ line string }
type execDoneMsg struct {
	res     execx.Result
	command string
}

type sessTurn struct {
	Prompt   string
	Commands []string
	Exit     int
}

type replModel struct {
	cfg      config.Config
	force    bool
	osStr    string
	shell    string
	cwd      string
	lsTop    []string
	histFile []string

	vp    viewport.Model
	input textinput.Model
	ready bool

	lines    []string // styled transcript
	streamed string   // live raw stream buffer (shown dim, replaced on DONE)
	status   string

	m mode

	// active prompt lifecycle
	prompt    string
	turn      int
	userCtx   string
	raw       string
	pending   string // command awaiting confirm
	pendingWhy string
	sess      []sessTurn

	inputHist []string
	histIdx   int

	prog   *tea.Program
	cancel context.CancelFunc

	width, height int
}

func RunREPL(cfg config.Config, osStr, shell, cwd string, lsTop, histFile []string, force bool) error {
	m := &replModel{
		cfg: cfg, force: force, osStr: osStr, shell: shell, cwd: cwd,
		lsTop: lsTop, histFile: histFile, m: modeInput, histIdx: -1,
	}
	m.lines = append(m.lines, styleHeader(cfg.UI.Color,
		fmt.Sprintf("argon REPL — %s/%s — %s — type English, /help for commands", osStr, shell, cwd)))
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.prog = p
	_, err := p.Run()
	return err
}

func (m *replModel) Init() tea.Cmd { return textinput.Blink }

func (m *replModel) header() string {
	return styleHeader(m.cfg.UI.Color, fmt.Sprintf(
		"argon · %s/%s · %s · %s/%s · turn %d/%d%s",
		m.osStr, m.shell, m.cwd, m.cfg.LLM.Preset, m.cfg.LLM.Model,
		m.turn+1, m.cfg.Defaults.MaxTurns, historyBadge(m),
	))
}

func historyBadge(m *replModel) string {
	if m.cfg.Defaults.History {
		return fmt.Sprintf(" · +history(%d)", len(m.histFile))
	}
	return ""
}

func (m *replModel) render() {
	body := strings.Join(m.lines, "\n")
	if m.streamed != "" {
		body += "\n" + styleDim(m.cfg.UI.Color, m.streamed)
	}
	m.vp.SetContent(body)
	m.vp.GotoBottom()
}

func (m *replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.ready {
			m.input = textinput.New()
			m.input.Placeholder = "describe a task in English, or /help"
			m.input.CharLimit = 2000
			m.input.Width = msg.Width - 4
			m.input.Focus()
			m.vp = viewport.New(msg.Width, msg.Height-5)
			m.vp.SetContent(strings.Join(m.lines, "\n"))
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - 5
			m.input.Width = msg.Width - 4
		}
		return m, nil

	case chunkMsg:
		if m.m == modeStreaming {
			m.raw += msg.text
			m.streamed += msg.text
			if len(m.streamed) > 4000 {
				m.streamed = m.streamed[len(m.streamed)-4000:]
			}
			m.render()
		}
		return m, nil

	case doneMsg:
		if m.m != modeStreaming {
			return m, nil
		}
		m.streamed = ""
		m.handleLLMDone(msg.raw)
		return m, nil

	case errMsg:
		m.streamed = ""
		m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "llm error: "+msg.err.Error()))
		m.m = modeInput
		m.input.Focus()
		m.render()
		return m, nil

	case execLineMsg:
		m.lines = append(m.lines, msg.line)
		m.render()
		return m, nil

	case execDoneMsg:
		m.handleExecDone(msg)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.ready {
		var c1, c2 tea.Cmd
		m.vp, c1 = m.vp.Update(msg)
		if m.m == modeInput || m.m == modeConfirmDestr {
			m.input, c2 = m.input.Update(msg)
		}
		return m, tea.Batch(c1, c2)
	}
	return m, nil
}

func (m *replModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.m == modeStreaming || m.m == modeRunning {
			if m.cancel != nil {
				m.cancel()
			}
			m.streamed = ""
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(cancelled)"))
			m.m = modeInput
			m.input.Focus()
			m.render()
			return m, nil
		}
		if m.input.Value() == "" {
			return m, tea.Quit
		}
		m.input.SetValue("")
		return m, nil
	case tea.KeyCtrlD:
		return m, tea.Quit
	case tea.KeyEsc:
		if m.m == modeConfirmWrite {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(aborted)"))
			m.m = modeInput
			m.pending = ""
			m.input.Focus()
			m.render()
			return m, nil
		}
		return m, nil
	case tea.KeyEnter:
		if m.m == modeConfirmWrite {
			m.runPending()
			return m, nil
		}
		if m.m != modeInput && m.m != modeConfirmDestr {
			return m, nil
		}
		val := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		if val == "" {
			return m, nil
		}
		if m.m == modeConfirmDestr {
			if val == "YES" {
				m.runPending()
			} else {
				m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(aborted — type YES to confirm destructive)"))
				m.m = modeInput
				m.pending = ""
				m.input.Focus()
				m.render()
			}
			return m, nil
		}
		// modeInput
		m.inputHist = append(m.inputHist, val)
		m.histIdx = -1
		if strings.HasPrefix(val, "/") {
			m.handleSlash(val)
			m.render()
			return m, nil
		}
		m.startPrompt(val)
		m.render()
		return m, nil
	case tea.KeyUp:
		if m.m == modeInput && len(m.inputHist) > 0 {
			if m.histIdx == -1 {
				m.histIdx = len(m.inputHist) - 1
			} else if m.histIdx > 0 {
				m.histIdx--
			}
			m.input.SetValue(m.inputHist[m.histIdx])
		}
		return m, nil
	case tea.KeyDown:
		if m.m == modeInput && m.histIdx != -1 {
			if m.histIdx >= len(m.inputHist)-1 {
				m.histIdx = -1
				m.input.SetValue("")
			} else {
				m.histIdx++
				m.input.SetValue(m.inputHist[m.histIdx])
			}
		}
		return m, nil
	case tea.KeyTab:
		if m.m == modeInput {
			m.complete()
		}
		return m, nil
	}

	// single-key confirm for WRITE: y/n while blurred
	if m.m == modeConfirmWrite && msg.Type == tea.KeyRunes {
		r := msg.String()
		if r == "y" || r == "Y" {
			m.runPending()
			return m, nil
		}
		if r == "n" || r == "N" {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(aborted)"))
			m.m = modeInput
			m.pending = ""
			m.input.Focus()
			m.render()
			return m, nil
		}
	}

	if m.ready && (m.m == modeInput || m.m == modeConfirmDestr) {
		var c tea.Cmd
		m.input, c = m.input.Update(msg)
		_ = c
	}
	if m.ready {
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	}
	return m, nil
}

func (m *replModel) complete() {
	v := m.input.Value()
	if strings.HasPrefix(v, "/") {
		cands := []string{"/help", "/clear", "/shell", "/model", "/provider", "/verbose", "/dry-run", "/stream", "/history", "/force", "/settings", "/quit"}
		var hits []string
		for _, c := range cands {
			if strings.HasPrefix(c, v) {
				hits = append(hits, c)
			}
		}
		if len(hits) == 1 {
			m.input.SetValue(hits[0] + " ")
		} else if len(hits) > 1 {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, strings.Join(hits, "  ")))
			m.render()
		}
		return
	}
	ents, err := os.ReadDir(m.cwd)
	if err != nil {
		return
	}
	tail := v
	if i := strings.LastIndexAny(v, " /\\"); i >= 0 {
		tail = v[i+1:]
	}
	var hits []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), tail) {
			hits = append(hits, e.Name())
		}
	}
	sort.Strings(hits)
	if len(hits) == 1 {
		m.input.SetValue(v + hits[0][len(tail):])
	} else if len(hits) > 1 {
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, strings.Join(hits, "  ")))
		m.render()
	}
}

var slashHelp = `/help — this help
/clear — clear view + session turns (file history kept if on)
/shell [x] · /model [x] · /provider [x] — show or set
/verbose · /dry-run [on|off] · /stream [on|off] · /history [on|off] · /force [on|off]
/settings — opens external settings (Stage 4 ships in-TUI editor)
/quit — exit (Ctrl+D, Ctrl+C on empty line)`

func (m *replModel) handleSlash(val string) {
	f := strings.Fields(val)
	cmd := strings.ToLower(f[0])
	arg := ""
	if len(f) > 1 {
		arg = strings.Join(f[1:], " ")
	}
	switch cmd {
	case "/help", "/h", "/?":
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, slashHelp))
	case "/quit", "/exit", "/q":
		if m.prog != nil {
			// quit asynchronously to avoid deadlock inside Update
			go m.prog.Quit()
		}
	case "/clear":
		m.lines = nil
		m.sess = nil
		m.turn = 0
		m.streamed, m.raw, m.pending = "", "", ""
		m.m = modeInput
		m.lines = append(m.lines, styleHeader(m.cfg.UI.Color, "cleared."))
	case "/shell":
		if arg == "" {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "shell="+m.shell))
		} else {
			m.shell = strings.ToLower(arg)
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "shell → "+m.shell))
		}
	case "/model":
		if arg == "" {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "model="+m.cfg.LLM.Model))
		} else {
			m.cfg.LLM.Model = arg
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "model → "+arg))
		}
	case "/provider":
		if arg == "" {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "provider="+m.cfg.LLM.Preset))
		} else {
			old := llm.Lookup(m.cfg.LLM.Preset)
			m.cfg.LLM.Preset = strings.ToLower(arg)
			nw := llm.Lookup(m.cfg.LLM.Preset)
			if m.cfg.LLM.BaseURL == "" || m.cfg.LLM.BaseURL == old.BaseURL {
				m.cfg.LLM.BaseURL = nw.BaseURL
			}
			if m.cfg.LLM.Model == "" || m.cfg.LLM.Model == old.Model {
				m.cfg.LLM.Model = nw.Model
			}
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "provider → "+m.cfg.LLM.Preset))
		}
	case "/verbose":
		m.cfg.UI.Verbose = !m.cfg.UI.Verbose
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, fmt.Sprintf("verbose=%v", m.cfg.UI.Verbose)))
	case "/dry-run", "/dryrun":
		m.cfg.Defaults.ConfirmWrite = true
		if strings.ToLower(arg) == "off" {
			setDryRun(m, false)
		} else if strings.ToLower(arg) == "on" {
			setDryRun(m, true)
		} else {
			setDryRun(m, !dryRunOn(m))
		}
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, fmt.Sprintf("dry-run=%v", dryRunOn(m))))
	case "/stream":
		if strings.ToLower(arg) == "off" {
			m.cfg.UI.Stream = false
		} else if strings.ToLower(arg) == "on" {
			m.cfg.UI.Stream = true
		} else {
			m.cfg.UI.Stream = !m.cfg.UI.Stream
		}
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, fmt.Sprintf("stream=%v", m.cfg.UI.Stream)))
	case "/history":
		if strings.ToLower(arg) == "off" {
			m.cfg.Defaults.History = false
		} else if strings.ToLower(arg) == "on" {
			m.cfg.Defaults.History = true
		} else {
			m.cfg.Defaults.History = !m.cfg.Defaults.History
		}
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, fmt.Sprintf("history=%v", m.cfg.Defaults.History)))
	case "/force":
		if strings.ToLower(arg) == "off" {
			m.force = false
		} else if strings.ToLower(arg) == "on" {
			m.force = true
		} else {
			m.force = !m.force
		}
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, fmt.Sprintf("force=%v (destructive still needs YES)", m.force)))
	case "/settings":
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "run `argon settings` outside REPL (in-TUI editor ships Stage 4)"))
	default:
		m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "unknown command: "+cmd+" (try /help)"))
	}
}

// dry-run is represented as ConfirmWrite=true + dryRun flag stored in status;
// keep a dedicated bool via status string to avoid config churn: use dryRunOn.
func dryRunOn(m *replModel) bool { return m.status == "dry" }
func setDryRun(m *replModel, on bool) {
	if on {
		m.status = "dry"
	} else {
		m.status = ""
	}
}

func (m *replModel) startPrompt(p string) {
	m.prompt = p
	m.turn = 0
	m.lines = append(m.lines, styleHeader(m.cfg.UI.Color, "you: "+p))
	m.startLLMTurn(m.buildUserCtx(p, ""))
}

func (m *replModel) buildUserCtx(prompt, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "os=%s shell=%s cwd=%s\nls: %s", m.osStr, m.shell, m.cwd, strings.Join(m.lsTop, ", "))
	if m.cfg.Defaults.History && len(m.histFile) > 0 {
		b.WriteString("\nrecent shell history:\n" + strings.Join(m.histFile, "\n"))
	}
	// own last turns always (amnesia-proof)
	for _, t := range m.sess {
		if len(m.sess) > 5 {
			continue
		}
		b.WriteString(fmt.Sprintf("\n[prev prompt=%q commands=%v exit=%d]", t.Prompt, t.Commands, t.Exit))
	}
	b.WriteString("\n\ntask: " + risk.Redact(prompt))
	if extra != "" {
		b.WriteString("\n" + extra)
	}
	return b.String()
}

func (m *replModel) llmReq() (baseURL, model, key string, preset llm.Preset) {
	preset = llm.Lookup(m.cfg.LLM.Preset)
	baseURL = m.cfg.LLM.BaseURL
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	model = m.cfg.LLM.Model
	if model == "" {
		model = preset.Model
	}
	key = m.cfg.APIKey()
	return baseURL, model, key, preset
}

func (m *replModel) startLLMTurn(userCtx string) {
	m.userCtx = userCtx
	m.m = modeStreaming
	m.raw = ""
	m.streamed = ""
	baseURL, model, key, preset := m.llmReq()
	if key == "" {
		m.lines = append(m.lines, styleErr(m.cfg.UI.Color,
			fmt.Sprintf("no API key — set %s (or ARGON_API_KEY) or run `argon settings`", preset.KeyEnv)))
		m.m = modeInput
		m.input.Focus()
		return
	}
	m.status = fmt.Sprintf("turn %d…", m.turn+1)
	llmTimeout := time.Duration(m.cfg.Defaults.LLMTimeoutSec) * time.Second
	if llmTimeout <= 0 {
		llmTimeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	m.cancel = cancel
	p := m.prog
	sys := llm.SystemPrompt
	go func() {
		defer cancel()
		if !m.cfg.UI.Stream {
			raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx, Timeout: llmTimeout})
			if err != nil {
				p.Send(errMsg{err})
				return
			}
			p.Send(doneMsg{raw})
			return
		}
		ch := make(chan llm.Chunk, 64)
		go llm.StreamChat(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx}, ch)
		var sb strings.Builder
		var serr error
		for ck := range ch {
			if ck.Err != nil {
				serr = ck.Err
				break
			}
			sb.WriteString(ck.Text)
			p.Send(chunkMsg{ck.Text})
			if ck.Done {
				break
			}
		}
		if serr != nil {
			raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx, Timeout: llmTimeout})
			if err != nil {
				p.Send(errMsg{fmt.Errorf("stream failed (%v) + retry failed (%v)", serr, err)})
				return
			}
			p.Send(doneMsg{raw})
			return
		}
		if sb.Len() == 0 {
			raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx, Timeout: llmTimeout})
			if err != nil {
				p.Send(errMsg{err})
				return
			}
			p.Send(doneMsg{raw})
			return
		}
		p.Send(doneMsg{sb.String()})
	}()
}

func (m *replModel) handleLLMDone(raw string) {
	if m.cfg.UI.Verbose {
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "raw("+fmt.Sprint(len(raw))+"b)"))
	}
	reply, err := llm.ParseReply(raw)
	if err != nil {
		// one repair retry inline (blocking is fine, short)
		baseURL, model, key, _ := m.llmReq()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		raw2, err2 := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model,
			System:  llm.SystemPrompt,
			User:    llm.RepairPrompt + "\n\nBAD OUTPUT:\n" + raw,
			Timeout: 60 * time.Second})
		if err2 != nil {
			m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "could not parse model JSON: "+err.Error()))
			m.m = modeInput
			m.input.Focus()
			m.render()
			return
		}
		var err3 error
		reply, err3 = llm.ParseReply(raw2)
		if err3 != nil {
			m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "could not parse model JSON: "+err3.Error()))
			m.m = modeInput
			m.input.Focus()
			m.render()
			return
		}
	}
	if reply.Thought != "" {
		m.lines = append(m.lines, styleArgon(m.cfg.UI.Color, reply.Thought))
	}
	if reply.Done || reply.Command == nil || strings.TrimSpace(*reply.Command) == "" {
		m.lines = append(m.lines, reply.Message)
		m.finishPrompt(nil, 0)
		m.render()
		return
	}
	command := strings.TrimSpace(*reply.Command)
	if execx.IsPureCD(command) {
		m.cwd = tuiResolveCD(m.cwd, command)
		m.lines = append(m.lines, styleCmd(m.cfg.UI.Color, command+"  (cwd → "+m.cwd+")"))
		m.userCtx = fmt.Sprintf("(cd → %s)\n%s", m.cwd, m.userCtx)
		m.render()
		m.nextTurn("(cd ok)")
		return
	}
	tier, why := risk.Classify(command, m.cfg.Risk.ExtraDeny, m.cfg.Risk.ExtraDestructive, disabledSet(m.cfg.Risk.Disabled))
	m.lines = append(m.lines, styleCmd(m.cfg.UI.Color, command)+styleDim(m.cfg.UI.Color, fmt.Sprintf("  [%s]", tier)))
	if m.cfg.UI.Verbose {
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "policy: "+why))
	}
	switch tier {
	case risk.TierDeny:
		m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "blocked: "+why))
		m.finishPrompt([]string{command}, 2)
		m.m = modeInput
		m.input.Focus()
	case risk.TierDestr:
		if !m.force {
			m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "destructive needs startup --force (or /force on) + typed YES. Aborted: "+why))
			m.finishPrompt([]string{command}, 2)
			m.m = modeInput
			m.input.Focus()
			break
		}
		m.pending, m.pendingWhy = command, why
		m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "destructive — type YES + Enter to run"))
		m.m = modeConfirmDestr
		m.input.Focus()
	case risk.TierWrite:
		if dryRunOn(m) {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(dry-run, not executed)"))
			m.finishPrompt([]string{command}, 0)
			m.m = modeInput
			m.input.Focus()
			break
		}
		m.pending, m.pendingWhy = command, why
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "run? [Enter=y / n]"))
		m.m = modeConfirmWrite
		m.input.Blur()
	default: // read
		if dryRunOn(m) {
			m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(dry-run, not executed)"))
			m.finishPrompt([]string{command}, 0)
			m.m = modeInput
			m.input.Focus()
			break
		}
		m.pending = command
		m.runPending()
	}
	m.render()
}

func (m *replModel) runPending() {
	command := m.pending
	m.pending = ""
	if command == "" {
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	m.m = modeRunning
	m.input.Blur()
	m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "running…"))
	m.render()
	execTimeout := time.Duration(m.cfg.Defaults.ExecTimeoutSec) * time.Second
	if execTimeout <= 0 {
		execTimeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	m.cancel = cancel
	p := m.prog
	cwd, shell := m.cwd, m.shell
	go func() {
		defer cancel()
		res := execx.Run(ctx, shell, command, cwd, execTimeout, func(line string) {
			p.Send(execLineMsg{strings.TrimRight(line, "\n")})
		})
		p.Send(execDoneMsg{res, command})
	}()
}

func (m *replModel) handleExecDone(msg execDoneMsg) {
	m.lines = append(m.lines, styleDim(m.cfg.UI.Color, fmt.Sprintf("(exit %d)", msg.res.ExitCode)))
	m.render()
	redacted := risk.Redact(msg.res.Output)
	// append output tail to transcript (already streamed live line-by-line;
	// if output was huge, execx truncated to 8k — show nothing extra)
	_ = redacted
	m.sess = append(m.sess, sessTurn{Prompt: m.prompt, Commands: []string{msg.command}, Exit: msg.res.ExitCode})
	if len(m.sess) > 20 {
		m.sess = m.sess[len(m.sess)-20:]
	}
	m.turn++
	if m.turn >= m.cfg.Defaults.MaxTurns {
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(max-turns reached)"))
		m.finishPrompt([]string{msg.command}, msg.res.ExitCode)
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	m.userCtx = fmt.Sprintf("command: %s\nexit: %d\noutput:\n%s", msg.command, msg.res.ExitCode, redacted)
	m.m = modeStreaming
	m.render()
	m.startLLMTurnContinue()
}

func (m *replModel) startLLMTurnContinue() {
	// continue same prompt lifecycle (turn already incremented)
	m.raw = ""
	m.streamed = ""
	baseURL, model, key, _ := m.llmReq()
	if key == "" {
		m.lines = append(m.lines, styleErr(m.cfg.UI.Color, "no API key"))
		m.m = modeInput
		m.input.Focus()
		return
	}
	m.status = fmt.Sprintf("turn %d…", m.turn+1)
	llmTimeout := time.Duration(m.cfg.Defaults.LLMTimeoutSec) * time.Second
	if llmTimeout <= 0 {
		llmTimeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	m.cancel = cancel
	p := m.prog
	sys, userCtx := llm.SystemPrompt, m.userCtx
	go func() {
		defer cancel()
		ch := make(chan llm.Chunk, 64)
		go llm.StreamChat(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx}, ch)
		var sb strings.Builder
		var serr error
		for ck := range ch {
			if ck.Err != nil {
				serr = ck.Err
				break
			}
			sb.WriteString(ck.Text)
			p.Send(chunkMsg{ck.Text})
			if ck.Done {
				break
			}
		}
		if serr != nil || sb.Len() == 0 {
			raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx, Timeout: llmTimeout})
			if err != nil {
				p.Send(errMsg{err})
				return
			}
			p.Send(doneMsg{raw})
			return
		}
		p.Send(doneMsg{sb.String()})
	}()
}

func (m *replModel) nextTurn(note string) {
	m.turn++
	if m.turn >= m.cfg.Defaults.MaxTurns {
		m.lines = append(m.lines, styleDim(m.cfg.UI.Color, "(max-turns reached)"))
		m.finishPrompt(nil, 0)
		m.m = modeInput
		m.input.Focus()
		return
	}
	_ = note
	m.startLLMTurnContinue()
}

func (m *replModel) finishPrompt(cmds []string, exit int) {
	m.sess = append(m.sess, sessTurn{Prompt: m.prompt, Commands: cmds, Exit: exit})
	m.m = modeInput
	m.status = ""
	m.input.Focus()
	appendReplHistory(m.prompt, cmds, exit)
}

func (m *replModel) View() string {
	if !m.ready {
		return "loading…"
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(m.vp.View() + "\n")
	prompt := "> "
	if m.m == modeConfirmWrite {
		prompt = "[Enter=y/n] "
	} else if m.m == modeConfirmDestr {
		prompt = "[type YES] "
	} else if m.m == modeStreaming {
		prompt = "… "
	} else if m.m == modeRunning {
		prompt = "◌ "
	}
	b.WriteString(prompt + m.input.View() + "\n")
	if m.status != "" && !dryRunOn(m) {
		b.WriteString(styleDim(m.cfg.UI.Color, m.status))
	} else if dryRunOn(m) {
		b.WriteString(styleDim(m.cfg.UI.Color, "dry-run"))
	}
	return b.String()
}

// --- local copies (avoid cli import cycle) ---

func disabledSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func tuiResolveCD(base, cmd string) string {
	s := strings.TrimSpace(cmd)
	if q := lastQuotedTUI(s); q != "" {
		return joinBaseTUI(base, q)
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return base
	}
	switch strings.ToLower(parts[0]) {
	case "cd", "chdir", "set-location", "sl", "pushd":
		t := ""
		for i := len(parts) - 1; i >= 1; i-- {
			a := strings.Trim(parts[i], `"';`)
			if a == "" || strings.HasPrefix(a, "-") {
				continue
			}
			t = strings.Trim(a, `"'`)
			break
		}
		if t == "" {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Clean(home)
			}
			return base
		}
		return joinBaseTUI(base, t)
	case "popd":
		return base
	}
	return base
}

func lastQuotedTUI(s string) string {
	for _, q := range []byte{'"', '\''} {
		parts := strings.Split(s, string(q))
		if len(parts) >= 3 {
			if inner := strings.TrimSpace(parts[len(parts)-2]); inner != "" {
				return inner
			}
		}
	}
	return ""
}

func joinBaseTUI(base, target string) string {
	target = strings.Trim(target, `"'`)
	if target == "~" || strings.HasPrefix(target, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if target == "~" {
				return filepath.Clean(home)
			}
			return filepath.Clean(filepath.Join(home, target[2:]))
		}
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\`) || (len(target) >= 2 && target[1] == ':') {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}

func appendReplHistory(prompt string, cmds []string, exit int) {
	d, err := config.Dir()
	if err != nil {
		return
	}
	_ = os.MkdirAll(d, 0o700)
	f, err := os.OpenFile(filepath.Join(d, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	e := map[string]any{"mode": "repl", "prompt": prompt, "commands": cmds, "exit": exit}
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
}
