// One-shot fullscreen runner (inline BubbleTea, scrollback-preserving).
//
// Used when stdout is a TTY: Thinking… counter while streaming, collapsible
// "thought for Ns" blocks (hover highlights, click or `t` toggles), commands
// in yellow panels, terminal output behind a grey edge, answers with a green
// tick. Stays open after finishing so thoughts can be expanded; `q` quits.
// Non-TTY keeps the plain printer in cli/oneshot.go.
package tui

import (
	"context"
	"fmt"
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

// --- transcript blocks -----------------------------------------------------

type osBlockKind int

const (
	bEcho osBlockKind = iota
	bThought
	bCmd
	bOut
	bMsg
	bErr
	bMeta
)

type osBlock struct {
	kind    osBlockKind
	thought *ThoughtBlock // bThought
	text    string        // bEcho, bMsg, bErr, bMeta
	cmd     string        // bCmd
	tier    string        // bCmd
	lines   []string      // bOut (live)
}

func (b *osBlock) render(color bool, width int) []string {
	switch b.kind {
	case bEcho:
		return []string{stHead(color).Render("you: " + b.text)}
	case bThought:
		return b.thought.lines(color, width)
	case bCmd:
		return commandBlock(color, b.cmd, b.tier, width)
	case bOut:
		return outputLines(color, b.lines)
	case bMsg:
		return messageLine(color, b.text, width)
	case bErr:
		out := []string{}
		for _, l := range wrap(b.text, width) {
			out = append(out, stErr(color).Render(l))
		}
		return out
	case bMeta:
		return []string{stMeta(color).Render(b.text)}
	}
	return nil
}

// --- runner messages -------------------------------------------------------

type osTok struct{ text string }
type osDone struct {
	raw   string
	reply llm.AgentReply
}
type osErr struct{ err error }
type osBegin struct{}
type osTick struct{}

// --- runner phases ---------------------------------------------------------

type osPhase int

const (
	phThink osPhase = iota
	phConfirmWrite
	phConfirmDestr
	phRun
	phEnd
)

type oneShotRunner struct {
	cfg                 config.Config
	force               bool
	osStr, shell, cwd   string
	lsTop, histFile     []string
	prompt              string
	dryRun, yesFlag     bool
	maxTurns            int
	llmTimeout, execTimeout time.Duration

	vp      viewport.Model
	confirm textinput.Model
	ready   bool
	width   int
	height  int

	blocks  []*osBlock
	phase   osPhase
	status  string
	turn    int
	userCtx string
	raw     string

	pending     string
	pendingWhy  string
	pendingDone bool
	pendingMsg  string

	sess     []sessTurn
	prog     *tea.Program
	cancel   context.CancelFunc
	finished bool
	exitCode int
	cancelArmed bool // first Ctrl+C cancels turn, second quits
}

func RunOneShotTUI(cfg config.Config, osStr, shell, cwd string, lsTop, histFile []string, prompt string, dryRun, yesFlag, forceFlag bool) error {
	m := &oneShotRunner{
		cfg: cfg, force: forceFlag,
		osStr: osStr, shell: shell, cwd: cwd,
		lsTop: lsTop, histFile: histFile,
		prompt: prompt, dryRun: dryRun, yesFlag: yesFlag,
		maxTurns: cfg.Defaults.MaxTurns,
	}
	m.llmTimeout = time.Duration(cfg.Defaults.LLMTimeoutSec) * time.Second
	if m.llmTimeout <= 0 {
		m.llmTimeout = 90 * time.Second
	}
	m.execTimeout = time.Duration(cfg.Defaults.ExecTimeoutSec) * time.Second
	if m.execTimeout <= 0 {
		m.execTimeout = 60 * time.Second
	}
	if m.maxTurns <= 0 {
		m.maxTurns = 8
	}
	m.blocks = append(m.blocks, &osBlock{kind: bEcho, text: prompt})
	p := tea.NewProgram(m, tea.WithMouseCellMotion())
	m.prog = p
	_, err := p.Run()
	return err
}

func (m *oneShotRunner) Init() tea.Cmd {
	return func() tea.Msg { return osBegin{} }
}

func (m *oneShotRunner) header() string {
	h := stHead(m.cfg.UI.Color).Render(fmt.Sprintf("◈ argon · %s/%s · %s", m.osStr, m.shell, m.cwd))
	if m.cfg.Defaults.History && len(m.histFile) > 0 {
		h += "  " + stMeta(m.cfg.UI.Color).Render(fmt.Sprintf("+history(%d)", len(m.histFile)))
	}
	return h
}

// spans returns each block's starting absolute line (blank separators count).
func (m *oneShotRunner) spans() ([]int, int) {
	spans := make([]int, 0, len(m.blocks))
	line := 0
	for i, b := range m.blocks {
		spans = append(spans, line)
		line += len(b.render(m.cfg.UI.Color, m.vp.Width))
		if i != len(m.blocks)-1 {
			line++ // blank separator
		}
	}
	return spans, line
}

func (m *oneShotRunner) render() {
	if !m.ready {
		return
	}
	var sb strings.Builder
	for i, b := range m.blocks {
		if i > 0 {
			sb.WriteString("\n")
		}
		for _, l := range b.render(m.cfg.UI.Color, m.vp.Width) {
			sb.WriteString(l + "\n")
		}
	}
	m.vp.SetContent(strings.TrimSuffix(sb.String(), "\n"))
	m.vp.GotoBottom()
}

func (m *oneShotRunner) footer() string {
	var f string
	switch m.phase {
	case phThink:
		f = stMeta(m.cfg.UI.Color).Render("working…  ctrl+c cancels turn")
	case phConfirmWrite:
		f = stCmd(m.cfg.UI.Color).Render("run?") + stMeta(m.cfg.UI.Color).Render("  [y/Enter=yes · n=no]")
	case phConfirmDestr:
		f = stErr(m.cfg.UI.Color).Render("destructive — type YES + Enter:") + " " + m.confirm.View()
	case phRun:
		f = stMeta(m.cfg.UI.Color).Render("running…  ctrl+c kills command")
	case phEnd:
		f = stMeta(m.cfg.UI.Color).Render("done · q to exit · t toggles last thought")
	}
	hints := stMeta(m.cfg.UI.Color).Render("click a thought to expand · t toggles")
	if m.phase == phEnd {
		return f + "\n" + hints
	}
	return f + "\n" + hints
}

func (m *oneShotRunner) View() string {
	if !m.ready {
		return "loading…"
	}
	return m.header() + "\n\n" + m.vp.View() + "\n" + m.footer()
}

func (m *oneShotRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.ready {
			m.confirm = textinput.New()
			m.confirm.CharLimit = 8
			m.confirm.Width = 10
			m.vp = viewport.New(msg.Width, msg.Height-6)
			m.ready = true
			m.render()
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - 6
			m.render()
		}
		return m, nil

	case osBegin:
		m.beginTurn(m.buildCtx(m.prompt, ""))
		return m, m.tick()

	case osTick:
		if m.phase == phThink || m.phase == phRun {
			m.render()
			return m, m.tick()
		}
		return m, nil

	case osTok:
		m.raw += msg.text
		return m, nil

	case osDone:
		m.finishThought()
		m.handleReply(msg.reply)
		return m, nil

	case osErr:
		m.finishThought()
		m.blocks = append(m.blocks, &osBlock{kind: bErr, text: "llm error: " + msg.err.Error()})
		m.finish(1)
		m.render()
		return m, nil

	case execLineMsg:
		if ob := m.outBlock(); ob != nil {
			ob.lines = append(ob.lines, msg.line)
			m.render()
		}
		return m, nil

	case execDoneMsg:
		m.handleExecDone(msg)
		return m, nil

	case tea.MouseMsg:
		m.handleMouse(msg)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.ready {
		var c1, c2 tea.Cmd
		m.vp, c1 = m.vp.Update(msg)
		if m.phase == phConfirmDestr {
			m.confirm, c2 = m.confirm.Update(msg)
			// Enter submits YES while confirming destructive.
			if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyEnter {
				return m.handleKey(key)
			}
		}
		return m, tea.Batch(c1, c2)
	}
	return m, nil
}

func (m *oneShotRunner) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return osTick{} })
}

// --- mouse: hover highlights, click toggles --------------------------------

func (m *oneShotRunner) blockAt(y int) *osBlock {
	contentY := y - 2 // header (1) + blank (1)
	if contentY < 0 || contentY >= m.vp.Height {
		return nil
	}
	spans, total := m.spans()
	idx := findBlock(spans, total, m.vp.YOffset+contentY)
	if idx < 0 {
		return nil
	}
	return m.blocks[idx]
}

func (m *oneShotRunner) clearHover() bool {
	changed := false
	for _, b := range m.blocks {
		if b.kind == bThought && b.thought.Hovered {
			b.thought.Hovered = false
			changed = true
		}
	}
	return changed
}

func (m *oneShotRunner) handleMouse(msg tea.MouseMsg) {
	switch msg.Action {
	case tea.MouseActionMotion:
		b := m.blockAt(msg.Y)
		was := m.clearHover()
		if b != nil && b.kind == bThought && b.thought.Done {
			b.thought.Hovered = true
			m.render()
		} else if was {
			m.render()
		}
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return
		}
		if b := m.blockAt(msg.Y); b != nil && b.kind == bThought && b.thought.Done {
			b.thought.Expanded = !b.thought.Expanded
			m.render()
		}
	}
}

// --- keyboard --------------------------------------------------------------

func (m *oneShotRunner) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// destructive confirm reads the input field, not single keys.
	if m.phase == phConfirmDestr {
		if msg.Type == tea.KeyEnter {
			if strings.TrimSpace(m.confirm.Value()) == "YES" {
				m.confirm.Blur()
				m.runPending()
			} else {
				m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(aborted — type YES to run destructive)"})
				m.finish(1)
				m.render()
			}
			return m, nil
		}
		if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
			m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(aborted)"})
			m.finish(1)
			m.render()
			return m, nil
		}
		var c tea.Cmd
		m.confirm, c = m.confirm.Update(msg)
		return m, c
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if (m.phase == phThink || m.phase == phRun) && !m.cancelArmed {
			if m.cancel != nil {
				m.cancel()
			}
			m.cancelArmed = true
			m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(cancelled)"})
			m.phase = phEnd
			m.status = ""
			m.render()
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyEsc:
		if m.phase == phConfirmWrite {
			m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(aborted)"})
			m.finish(1)
			m.render()
			return m, nil
		}
		return m, tea.Quit
	}

	if key := msg.String(); key == "q" || key == "Q" {
		return m, tea.Quit
	}
	if key := msg.String(); (key == "t" || key == "T") && m.phase != phConfirmWrite {
		m.toggleLastThought()
		m.render()
		return m, nil
	}

	if m.phase == phConfirmWrite && msg.Type == tea.KeyRunes {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.runPending()
			return m, nil
		case "n":
			m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(aborted — rerun with --yes to skip prompts)"})
			m.finish(1)
			m.render()
			return m, nil
		}
	}
	if m.phase == phConfirmWrite && msg.Type == tea.KeyEnter {
		m.runPending()
		return m, nil
	}
	return m, nil
}

func (m *oneShotRunner) toggleLastThought() {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == bThought && m.blocks[i].thought.Done {
			m.blocks[i].thought.Expanded = !m.blocks[i].thought.Expanded
			return
		}
	}
}

// --- agent loop (mirrors cli/oneshot orchestration) -------------------------

func (m *oneShotRunner) outBlock() *osBlock {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == bOut {
			return m.blocks[i]
		}
	}
	return nil
}

func (m *oneShotRunner) buildCtx(prompt, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "os=%s shell=%s cwd=%s\nls: %s", m.osStr, m.shell, m.cwd, strings.Join(m.lsTop, ", "))
	if m.cfg.Defaults.History && len(m.histFile) > 0 {
		b.WriteString("\nrecent shell history:\n" + strings.Join(m.histFile, "\n"))
	}
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

func (m *oneShotRunner) llmReq() (baseURL, model, key string, preset llm.Preset) {
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

func (m *oneShotRunner) beginTurn(userCtx string) {
	m.userCtx = userCtx
	m.phase = phThink
	m.raw = ""
	m.cancelArmed = false
	th := &ThoughtBlock{StartedAt: time.Now(), Expanded: m.cfg.UI.Verbose}
	m.blocks = append(m.blocks, &osBlock{kind: bThought, thought: th})
	m.render()

	baseURL, model, key, preset := m.llmReq()
	if key == "" {
		th.Done, th.Elapsed = true, 0
		m.blocks = append(m.blocks, &osBlock{kind: bErr,
			text: fmt.Sprintf("no API key — get a free Groq key at %s, then `argon settings` (or set %s)", llm.GroqKeyURL, preset.KeyEnv)})
		m.finish(1)
		m.render()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.llmTimeout)
	m.cancel = cancel
	p := m.prog
	sys := llm.SystemPrompt
	go func() {
		defer cancel()
		ch := make(chan llm.Chunk, 64)
		go llm.StreamChat(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx}, ch)
		var sb strings.Builder
		for ck := range ch {
			if ck.Err != nil {
				raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx, Timeout: m.llmTimeout})
				if err != nil {
					p.Send(osErr{fmt.Errorf("stream failed (%v) + retry failed (%v)", ck.Err, err)})
					return
				}
				m.deliver(raw)
				return
			}
			sb.WriteString(ck.Text)
			p.Send(osTok{ck.Text})
			if ck.Done {
				break
			}
		}
		if sb.Len() == 0 {
			raw, err := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model, System: sys, User: userCtx, Timeout: m.llmTimeout})
			if err != nil {
				p.Send(osErr{err})
				return
			}
			m.deliver(raw)
			return
		}
		m.deliver(sb.String())
	}()
}

// deliver parses (with one repair retry) off the UI thread, then hands over.
func (m *oneShotRunner) deliver(raw string) {
	reply, err := llm.ParseReply(raw)
	if err != nil {
		baseURL, model, key, _ := m.llmReq()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		raw2, err2 := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model,
			System: llm.SystemPrompt, User: llm.RepairPrompt + "\n\nBAD OUTPUT:\n" + raw, Timeout: 60 * time.Second})
		if err2 != nil {
			m.prog.Send(osErr{fmt.Errorf("could not parse model JSON: %v", err)})
			return
		}
		var err3 error
		reply, err3 = llm.ParseReply(raw2)
		if err3 != nil {
			m.prog.Send(osErr{fmt.Errorf("could not parse model JSON: %v", err3)})
			return
		}
		raw = raw2
	}
	m.prog.Send(osDone{raw, reply})
}

func (m *oneShotRunner) finishThought() {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == bThought && !m.blocks[i].thought.Done {
			th := m.blocks[i].thought
			th.Done = true
			th.Elapsed = time.Since(th.StartedAt)
			// Text is filled in by handleReply right after (or stays empty on error).
			return
		}
	}
}

func (m *oneShotRunner) handleReply(reply llm.AgentReply) {
	// set the thought text on the active (just-finished) block
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == bThought && m.blocks[i].thought.Done && m.blocks[i].thought.Text == "" {
			m.blocks[i].thought.Text = reply.Thought
			m.blocks[i].thought.Expanded = m.cfg.UI.Verbose
			break
		}
	}
	if reply.Command == nil || strings.TrimSpace(*reply.Command) == "" {
		m.blocks = append(m.blocks, &osBlock{kind: bMsg, text: reply.Message})
		m.finishPrompt(nil, 0)
		m.finish(0)
		m.render()
		return
	}
	m.pendingDone, m.pendingMsg = reply.Done, reply.Message
	command := strings.TrimSpace(*reply.Command)
	if execx.IsPureCD(command) {
		m.cwd = tuiResolveCD(m.cwd, command)
		m.blocks = append(m.blocks, &osBlock{kind: bCmd, cmd: command, tier: "cd"})
		m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "cwd → " + m.cwd})
		if m.pendingDone {
			m.blocks = append(m.blocks, &osBlock{kind: bMsg, text: reply.Message})
			m.finishPrompt([]string{command}, 0)
			m.finish(0)
			m.render()
			return
		}
		m.userCtx = fmt.Sprintf("(cd → %s)\n%s", m.cwd, m.userCtx)
		m.render()
		m.nextTurn()
		return
	}
	disabled := map[string]bool{}
	for _, id := range m.cfg.Risk.Disabled {
		disabled[id] = true
	}
	tier, why := risk.Classify(command, m.cfg.Risk.ExtraDeny, m.cfg.Risk.ExtraDestructive, disabled)
	m.blocks = append(m.blocks, &osBlock{kind: bCmd, cmd: command, tier: string(tier)})
	if m.cfg.UI.Verbose {
		m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "policy: " + why})
	}
	switch tier {
	case risk.TierDeny:
		m.blocks = append(m.blocks, &osBlock{kind: bErr, text: "blocked: " + why})
		m.finishPrompt([]string{command}, 2)
		m.finish(2)
	case risk.TierDestr:
		if !m.force {
			m.blocks = append(m.blocks, &osBlock{kind: bErr, text: "destructive needs --force + typed YES. Aborted: " + why})
			m.finishPrompt([]string{command}, 2)
			m.finish(2)
			break
		}
		m.pending, m.pendingWhy = command, why
		m.phase = phConfirmDestr
		m.confirm.Focus()
		m.confirm.SetValue("")
	case risk.TierWrite:
		if m.dryRun {
			m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(dry-run, not executed)"})
			m.finishPrompt([]string{command}, 0)
			m.finish(0)
			break
		}
		m.pending, m.pendingWhy = command, why
		if m.yesFlag {
			m.runPending()
			break
		}
		m.phase = phConfirmWrite
	default: // read
		if m.dryRun {
			m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(dry-run, not executed)"})
			m.finishPrompt([]string{command}, 0)
			m.finish(0)
			break
		}
		m.pending = command
		m.runPending()
	}
	m.render()
}

func (m *oneShotRunner) runPending() {
	command := m.pending
	m.pending = ""
	if command == "" {
		m.phase = phEnd
		m.render()
		return
	}
	m.phase = phRun
	m.cancelArmed = false
	m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "running…"})
	m.blocks = append(m.blocks, &osBlock{kind: bOut})
	m.render()
	ctx, cancel := context.WithTimeout(context.Background(), m.execTimeout)
	m.cancel = cancel
	p := m.prog
	cwd, shell := m.cwd, m.shell
	go func() {
		defer cancel()
		res := execx.Run(ctx, shell, command, cwd, m.execTimeout, func(line string) {
			p.Send(execLineMsg{strings.TrimRight(line, "\n")})
		})
		p.Send(execDoneMsg{res, command})
	}()
}

func (m *oneShotRunner) handleExecDone(msg execDoneMsg) {
	m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: fmt.Sprintf("(exit %d)", msg.res.ExitCode)})
	redacted := risk.Redact(msg.res.Output)
	m.sess = append(m.sess, sessTurn{Prompt: m.prompt, Commands: []string{msg.command}, Exit: msg.res.ExitCode})
	if len(m.sess) > 20 {
		m.sess = m.sess[len(m.sess)-20:]
	}
	if m.pendingDone {
		m.pendingDone = false
		if m.pendingMsg != "" {
			m.blocks = append(m.blocks, &osBlock{kind: bMsg, text: m.pendingMsg})
		}
		m.finishPrompt([]string{msg.command}, msg.res.ExitCode)
		m.finish(msg.res.ExitCode)
		m.render()
		return
	}
	m.turn++
	if m.turn >= m.maxTurns {
		m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(max-turns reached)"})
		m.finishPrompt([]string{msg.command}, msg.res.ExitCode)
		m.finish(msg.res.ExitCode)
		m.render()
		return
	}
	m.userCtx = fmt.Sprintf("command: %s\nexit: %d\noutput:\n%s", msg.command, msg.res.ExitCode, redacted)
	m.render()
	m.beginTurn(m.userCtx)
}

func (m *oneShotRunner) nextTurn() {
	m.turn++
	if m.turn >= m.maxTurns {
		m.blocks = append(m.blocks, &osBlock{kind: bMeta, text: "(max-turns reached)"})
		m.finish(0)
		m.render()
		return
	}
	m.beginTurn(m.userCtx)
}

func (m *oneShotRunner) finishPrompt(cmds []string, exit int) {
	m.sess = append(m.sess, sessTurn{Prompt: m.prompt, Commands: cmds, Exit: exit})
	appendHistoryTUI("oneshot", m.prompt, cmds, exit)
}

func (m *oneShotRunner) finish(code int) {
	m.finished = true
	m.exitCode = code
	_ = code
	m.phase = phEnd
	m.status = ""
	if m.cancel != nil {
		m.cancel()
	}
}
