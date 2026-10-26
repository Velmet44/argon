// Package tui hosts the fullscreen `argon` REPL (BubbleTea).
// States: input -> streaming -> confirming? -> running -> streaming... -> input.
// LLM tokens stream live via Program.Send; command output streams live too.
// Ctrl+C cancels the active turn, never kills the REPL (Ctrl+D or /quit exits).
package tui

import (
	"context"
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
	"github.com/Velmet44/argon/internal/history"
	"github.com/Velmet44/argon/internal/llm"
	"github.com/Velmet44/argon/internal/risk"
	"github.com/Velmet44/argon/internal/xcontext"
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
	Digest   string // local output-shape summary (never model-written)
	Note     string // model's own prior message for this step
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

	blocks []*rBlock // styled transcript (blank line between blocks)
	status string
	stick  bool // follow tail on render (cleared when user scrolls up)

	m mode

	// active prompt lifecycle
	prompt    string
	turn      int
	userCtx   string
	raw       string
	pending   string // command awaiting confirm
	pendingWhy string
	pendingDone bool
	pendingMsg  string
	lastTier  string // tier of the last classified command (for verify tracking)
	expectVerify bool // previous turn modified the system; next must verify
	enforceOnce bool // one engine-forced verification turn allowed per write
	lastDigest string // set before recordStep
	lastNote   string
	sess      []sessTurn
	curOut    *rBlock // live command output block
	taskCmds  []string // finished-task transcript: every considered command
	taskOuts  []string // parallel outputs ("" until executed)
	outIdx    int      // index into taskOuts for the in-flight command

	inputHist []string
	histIdx   int

	prog   *tea.Program
	cancel context.CancelFunc

	width, height int
}

type tickMsg struct{}

func (m *replModel) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func RunREPL(cfg config.Config, osStr, shell, cwd string, lsTop, histFile []string, force bool) error {
	m := &replModel{
		cfg: cfg, force: force, osStr: osStr, shell: shell, cwd: cwd,
		lsTop: lsTop, histFile: histFile, m: modeInput, histIdx: -1,
		stick: true,
	}
	m.blocks = append(m.blocks, &rBlock{kind: rHead, text:
		fmt.Sprintf("argon REPL — %s/%s — %s — type English, /help for commands", osStr, shell, cwd)})
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
	if m.cfg.History.ShellHistory {
		return fmt.Sprintf(" · +history(%d)", len(m.histFile))
	}
	return ""
}

func (m *replModel) render() {
	if !m.ready {
		return
	}
	width := m.vp.Width
	if width <= 0 {
		width = m.width
	}
	var sb strings.Builder
	for i, b := range m.blocks {
		if i > 0 {
			sb.WriteString("\n")
		}
		for _, l := range b.render(m.cfg.UI.Color, width) {
			sb.WriteString(l + "\n")
		}
	}
	m.vp.SetContent(strings.TrimSuffix(sb.String(), "\n"))
	if m.stick {
		m.vp.GotoBottom()
	}
}

// --- transcript append helpers (all set stick so new content is followed) ---

func (m *replModel) add(b *rBlock) {
	m.blocks = append(m.blocks, b)
	m.stick = true
}

func (m *replModel) addEcho(text string)  { m.add(&rBlock{kind: rEcho, text: text}) }
func (m *replModel) addHead(text string)  { m.add(&rBlock{kind: rHead, text: text}) }
func (m *replModel) addMeta(text string)  { m.add(&rBlock{kind: rMeta, text: text}) }
func (m *replModel) addMsg(text string)   { m.add(&rBlock{kind: rMsg, text: text}) }
func (m *replModel) addErr(text string)   { m.add(&rBlock{kind: rErr, text: text}) }
func (m *replModel) addCmd(cmd, tier string) { m.add(&rBlock{kind: rCmd, cmd: cmd, tier: tier}) }

func (m *replModel) addThought() *ThoughtBlock {
	th := &ThoughtBlock{StartedAt: time.Now(), Expanded: m.cfg.UI.Verbose}
	th.Hovered = false
	m.add(&rBlock{kind: rThought, thought: th})
	return th
}

func (m *replModel) addOut() *rBlock {
	b := &rBlock{kind: rOut}
	m.add(b)
	m.curOut = b
	return b
}

// finishActiveThought freezes the live Thinking… block (sets elapsed; text
// arrives in handleLLMDone).
func (m *replModel) finishActiveThought() {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if b := m.blocks[i]; b.kind == rThought && !b.thought.Done {
			b.thought.Done = true
			b.thought.Elapsed = time.Since(b.thought.StartedAt)
			return
		}
	}
}

// toggleLastCollapsibleBlock flips the newest thought/output block (F2).
func (m *replModel) toggleLastCollapsibleBlock() bool {
	if toggleLastCollapsible(m.blocks) {
		m.render()
		return true
	}
	return false
}

// mouse hit-testing: header occupies 2 rows (title + blank).
func (m *replModel) blockAt(y int) *rBlock {
	contentY := y - 2
	if contentY < 0 || contentY >= m.vp.Height {
		return nil
	}
	spans, total := blockSpans(m.blocks, m.cfg.UI.Color, m.vp.Width)
	idx := findBlock(spans, total, m.vp.YOffset+contentY)
	if idx < 0 {
		return nil
	}
	return m.blocks[idx]
}

func (m *replModel) handleMouse(msg tea.MouseMsg) {
	switch msg.Action {
	case tea.MouseActionMotion:
		b := m.blockAt(msg.Y)
		was := clearHover(m.blocks)
		if b != nil {
			if isThoughtBlock(b) {
				b.thought.Hovered = true
				m.render()
				return
			}
			if isOutBlock(b) && len(b.out) > outPreview && !b.outOpen {
				b.outHover = true
				m.render()
				return
			}
		}
		if was {
			m.render()
		}
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return
		}
		if b := m.blockAt(msg.Y); b != nil {
			if isThoughtBlock(b) {
				b.thought.Expanded = !b.thought.Expanded
				m.render()
			} else if isOutBlock(b) && len(b.out) > outPreview {
				b.outOpen = !b.outOpen
				m.render()
			}
		}
	default:
		// wheel + anything else: viewport scrolls, stop following tail.
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			m.stick = false
		}
	}
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
			m.ready = true
			m.render()
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - 5
			m.input.Width = msg.Width - 4
			m.render()
		}
		return m, nil

	case tickMsg:
		if m.m == modeStreaming || m.m == modeRunning {
			m.render()
			return m, m.tick()
		}
		return m, nil

	case chunkMsg:
		if m.m == modeStreaming {
			m.raw += msg.text
		}
		return m, nil

	case doneMsg:
		if m.m != modeStreaming {
			return m, nil
		}
		m.finishActiveThought()
		m.handleLLMDone(msg.raw)
		return m, nil

	case errMsg:
		m.finishActiveThought()
		m.addErr("llm error: " + msg.err.Error())
		m.m = modeInput
		m.input.Focus()
		m.render()
		return m, nil

	case execLineMsg:
		if m.curOut != nil {
			m.curOut.out = append(m.curOut.out, msg.line)
			m.stick = true
			m.render()
		}
		return m, nil

	case execDoneMsg:
		m.handleExecDone(msg)
		return m, nil

	case tea.MouseMsg:
		m.handleMouse(msg)
		// let the viewport also see wheel events for scrolling
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			var c tea.Cmd
			m.vp, c = m.vp.Update(msg)
			return m, c
		}
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
			m.finishActiveThought()
			m.addMeta("(cancelled)")
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
			m.addMeta("(aborted)")
			m.m = modeInput
			m.pending = ""
			m.input.Focus()
			m.render()
			return m, nil
		}
		return m, nil
	case tea.KeyF2:
		m.toggleLastCollapsibleBlock()
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
				m.addMeta("(aborted — type YES to confirm destructive)")
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
			m.addMeta("(aborted)")
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
			m.addMeta(strings.Join(hits, "  "))
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
		m.addMeta(strings.Join(hits, "  "))
		m.render()
	}
}

var slashHelp = `/help — this help
/clear — clear view + session turns (file history kept if on)
/shell [x] · /model [x] · /provider [x] — show or set
/verbose · /dry-run [on|off] · /stream [on|off] · /history [on|off] · /force [on|off]
/settings — full settings (same as: argon settings)
click a thought/output to expand · F2 toggles last block
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
		m.addMeta(slashHelp)
	case "/quit", "/exit", "/q":
		if m.prog != nil {
			// quit asynchronously to avoid deadlock inside Update
			go m.prog.Quit()
		}
	case "/clear":
		m.blocks = nil
		m.sess = nil
		m.turn = 0
		m.raw, m.pending, m.curOut = "", "", nil
		m.m = modeInput
		m.addHead("cleared.")
	case "/shell":
		if arg == "" {
			m.addMeta("shell=" + m.shell)
		} else {
			m.shell = strings.ToLower(arg)
			m.addMeta("shell → " + m.shell)
		}
	case "/model":
		if arg == "" {
			m.addMeta("model=" + m.cfg.LLM.Model)
		} else {
			m.cfg.LLM.Model = arg
			m.addMeta("model → " + arg)
		}
	case "/provider":
		if arg == "" {
			m.addMeta("provider=" + m.cfg.LLM.Preset)
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
			m.addMeta("provider → " + m.cfg.LLM.Preset)
		}
	case "/verbose":
		m.cfg.UI.Verbose = !m.cfg.UI.Verbose
		m.addMeta(fmt.Sprintf("verbose=%v", m.cfg.UI.Verbose))
	case "/dry-run", "/dryrun":
		m.cfg.Defaults.ConfirmWrite = true
		if strings.ToLower(arg) == "off" {
			setDryRun(m, false)
		} else if strings.ToLower(arg) == "on" {
			setDryRun(m, true)
		} else {
			setDryRun(m, !dryRunOn(m))
		}
		m.addMeta(fmt.Sprintf("dry-run=%v", dryRunOn(m)))
	case "/stream":
		if strings.ToLower(arg) == "off" {
			m.cfg.UI.Stream = false
		} else if strings.ToLower(arg) == "on" {
			m.cfg.UI.Stream = true
		} else {
			m.cfg.UI.Stream = !m.cfg.UI.Stream
		}
		m.addMeta(fmt.Sprintf("stream=%v", m.cfg.UI.Stream))
	case "/history":
		if strings.ToLower(arg) == "off" {
			m.cfg.History.ShellHistory = false
		} else if strings.ToLower(arg) == "on" {
			m.cfg.History.ShellHistory = true
		} else {
			m.cfg.History.ShellHistory = !m.cfg.History.ShellHistory
		}
		if m.cfg.History.ShellHistory {
			limit := m.cfg.History.ShellHistoryLimit
			if limit <= 0 {
				limit = 50
			}
			m.histFile = xcontext.ReadHistory(limit)
		} else {
			m.histFile = nil
		}
		m.addMeta(fmt.Sprintf("history=%v", m.cfg.History.ShellHistory))
	case "/force":
		if strings.ToLower(arg) == "off" {
			m.force = false
		} else if strings.ToLower(arg) == "on" {
			m.force = true
		} else {
			m.force = !m.force
		}
		m.addMeta(fmt.Sprintf("force=%v (destructive still needs YES)", m.force))
	case "/settings":
		m.addMeta("full settings live in `argon settings` (external TUI)")
	default:
		m.addErr("unknown command: " + cmd + " (try /help)")
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
	m.pendingDone, m.pendingMsg = false, ""
	m.lastDigest, m.lastNote = "", ""
	m.expectVerify, m.lastTier, m.enforceOnce = false, "", false
	m.taskCmds, m.taskOuts, m.outIdx = nil, nil, -1
	m.curOut = nil
	m.addEcho(p)
	m.startLLMTurn(m.buildFreshCtx())
}

func (m *replModel) buildFreshCtx() string {
	return m.buildCtx("", 0, false)
}

// buildCtx assembles one self-contained user message: original task verbatim
// every turn, STEPS ledger from local digests + prior notes (current task
// only), PREVIOUS TASKS from the on-disk transcript (cross-prompt memory),
// latest output. Shell history follows the [history] settings.
func (m *replModel) buildCtx(out string, exitCode int, hasOut bool) string {
	steps := make([]llm.Step, 0, len(m.sess))
	for _, t := range m.sess {
		if t.Prompt != m.prompt {
			continue // previous tasks live in PREVIOUS TASKS, not STEPS
		}
		steps = append(steps, llm.Step{
			Cmd:    strings.Join(t.Commands, " ; "),
			Exit:   t.Exit,
			Digest: t.Digest,
			Note:   t.Note,
		})
	}
	var prev []llm.PrevTask
	if m.cfg.History.RememberTurns {
		if p, err := history.RecentTasks(m.cfg.History.RememberLimit, m.cfg.History.RememberOutputChars); err == nil {
			prev = p
		}
	}
	var hist []string
	if m.cfg.History.ShellHistory {
		hist = m.histFile
	}
	return llm.BuildUserMessage(m.shell, m.osStr, m.cwd, strings.Join(m.lsTop, ", "),
		risk.Redact(m.prompt), hist, steps, prev, m.turn+1, m.maxTurns(), out, exitCode, m.expectVerify)
}

// recordStep appends one ledger entry (digest + note set by the caller first).
func (m *replModel) recordStep(cmds []string, exit int) {
	m.sess = append(m.sess, sessTurn{
		Prompt: m.prompt, Commands: cmds, Exit: exit,
		Digest: m.lastDigest, Note: m.lastNote,
	})
	m.lastDigest, m.lastNote = "", ""
	if len(m.sess) > 20 {
		m.sess = m.sess[len(m.sess)-20:]
	}
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
	m.curOut = nil
	th := &ThoughtBlock{StartedAt: time.Now(), Expanded: m.cfg.UI.Verbose}
	m.add(&rBlock{kind: rThought, thought: th})
	m.render()
	baseURL, model, key, preset := m.llmReq()
	if key == "" {
		th.Done, th.Elapsed, th.Text = true, 0, "(no key)"
		m.addErr(fmt.Sprintf("no API key — get a free Groq key at %s, then `argon settings` (or set %s)", llm.GroqKeyURL, preset.KeyEnv))
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	m.status = fmt.Sprintf("turn %d…", m.turn+1)
	llmTimeout := time.Duration(m.cfg.Defaults.LLMTimeoutSec) * time.Second
	if llmTimeout <= 0 {
		llmTimeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	m.cancel = cancel
	if m.prog != nil {
		m.prog.Send(tickMsg{}) // kick live Thinking… counter
	}
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
		m.addMeta("raw(" + fmt.Sprint(len(raw)) + "b)")
	}
	// freeze the live thought block first; text is set below on success
	th := m.activeThought()
	freeze := func(text string, expanded bool) {
		if th != nil {
			th.Done = true
			th.Elapsed = time.Since(th.StartedAt)
			th.Text = text
			th.Expanded = expanded
		}
	}
	reply, err := llm.ParseReply(raw)
	if err != nil {
		// one repair retry inline (blocking is fine, short): same system,
		// assistant echoes the bad reply, user carries the concrete error
		baseURL, model, key, _ := m.llmReq()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		bad := raw
		if len(bad) > 600 {
			bad = bad[:600]
		}
		raw2, err2 := llm.CompleteOnce(ctx, llm.TurnRequest{BaseURL: baseURL, APIKey: key, Model: model,
			System:  llm.SystemPrompt,
			History: []llm.Msg{{Role: "assistant", Content: bad}},
			User:    llm.RepairPromptFor(err.Error()),
			Timeout: 60 * time.Second})
		if err2 != nil {
			freeze("(parse failed)", false)
			m.addErr("could not parse model JSON: " + err.Error())
			m.m = modeInput
			m.input.Focus()
			m.render()
			return
		}
		var err3 error
		reply, err3 = llm.ParseReply(raw2)
		if err3 != nil {
			freeze("(parse failed)", false)
			m.addErr("could not parse model JSON: " + err3.Error())
			m.m = modeInput
			m.input.Focus()
			m.render()
			return
		}
	}
	freeze(reply.Thought, m.cfg.UI.Verbose)
	if reply.Command == nil || strings.TrimSpace(*reply.Command) == "" {
		if strings.TrimSpace(reply.Message) != "" {
			m.addMsg(reply.Message)
		}
		m.finishPrompt(nil, 0)
		m.render()
		return
	}
	doneFirst := reply.Done
	m.pendingDone, m.pendingMsg = doneFirst, reply.Message
	m.lastTier = ""
	command := strings.TrimSpace(*reply.Command)
	if execx.IsPureCD(command) {
		m.cwd = tuiResolveCD(m.cwd, command)
		m.addCmd(command, "cd")
		m.addMeta("cwd → " + m.cwd)
		m.lastDigest = "cwd now " + m.cwd
		m.lastNote = reply.Message
		m.taskCmds = append(m.taskCmds, command)
		m.taskOuts = append(m.taskOuts, "(directory changed)")
		m.render()
		if doneFirst {
			if strings.TrimSpace(reply.Message) != "" {
				m.addMsg(reply.Message)
			}
			m.recordStep([]string{command}, 0)
			m.finishPrompt([]string{command}, 0)
			m.m = modeInput
			m.input.Focus()
			m.render()
			return
		}
		m.recordStep([]string{command}, 0)
		m.nextTurn()
		return
	}
	tier, why := risk.Classify(command, m.cfg.Risk.ExtraDeny, m.cfg.Risk.ExtraDestructive, disabledSet(m.cfg.Risk.Disabled))
	m.lastTier = string(tier)
	m.addCmd(command, string(tier))
	if m.cfg.UI.Verbose {
		m.addMeta("policy: " + why)
	}
	if !reply.Run {
		// explicit model signal: display the plan, never execute it
		m.addMeta("(model asked not to run — displaying only)")
		if strings.TrimSpace(reply.Message) != "" {
			m.addMsg(reply.Message)
		}
		m.taskCmds = append(m.taskCmds, command)
		m.taskOuts = append(m.taskOuts, "")
		m.recordStep([]string{command}, 0)
		m.finishPrompt([]string{command}, 0)
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	m.taskCmds = append(m.taskCmds, command)
	m.taskOuts = append(m.taskOuts, "")
	m.outIdx = len(m.taskOuts) - 1
	switch tier {
	case risk.TierDeny:
		m.addErr("blocked: " + why)
		m.finishPrompt([]string{command}, 2)
		m.m = modeInput
		m.input.Focus()
	case risk.TierDestr:
		if !m.force {
			m.addErr("destructive needs startup --force (or /force on) + typed YES. Aborted: " + why)
			m.finishPrompt([]string{command}, 2)
			m.m = modeInput
			m.input.Focus()
			break
		}
		m.pending, m.pendingWhy = command, why
		m.addErr("destructive — type YES + Enter to run")
		m.m = modeConfirmDestr
		m.input.Focus()
	case risk.TierWrite:
		if dryRunOn(m) {
			m.addMeta("(dry-run — not executed)")
			m.finishPrompt([]string{command}, 0)
			m.m = modeInput
			m.input.Focus()
			break
		}
		m.pending, m.pendingWhy = command, why
		m.addMeta("run? [Enter=y / n]")
		m.m = modeConfirmWrite
		m.input.Blur()
	default: // read
		if dryRunOn(m) {
			m.addMeta("(dry-run — not executed)")
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

// activeThought returns the newest unfinished thought block, if any.
func (m *replModel) activeThought() *ThoughtBlock {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if b := m.blocks[i]; b.kind == rThought && !b.thought.Done {
			return b.thought
		}
	}
	return nil
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
	m.addMeta("running…")
	m.addOut()
	m.render()
	if m.prog != nil {
		m.prog.Send(tickMsg{}) // keep live views animating during exec
	}
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

// maxTurns centralizes the default so step numbering stays consistent.
func (m *replModel) maxTurns() int {
	if m.cfg.Defaults.MaxTurns <= 0 {
		return 8
	}
	return m.cfg.Defaults.MaxTurns
}

func (m *replModel) handleExecDone(msg execDoneMsg) {
	m.addMeta(fmt.Sprintf("(exit %d)", msg.res.ExitCode))
	m.render()
	redacted := risk.Redact(msg.res.Output)
	if m.curOut != nil && m.outIdx >= 0 && m.outIdx < len(m.taskOuts) {
		m.taskOuts[m.outIdx] = strings.Join(m.curOut.out, "\n")
	}
	m.lastDigest = llm.DigestOutput(redacted)
	m.lastNote = m.pendingMsg
	m.recordStep([]string{msg.command}, msg.res.ExitCode)
	m.expectVerify = m.lastTier == "write" || m.lastTier == "destructive"
	m.enforceOnce = true
	m.curOut = nil
	m.userCtx = m.buildCtx(redacted, msg.res.ExitCode, true)
	if m.pendingDone && !(m.expectVerify && m.enforceOnce && m.turn+1 < m.maxTurns()) {
		m.pendingDone = false
		if strings.TrimSpace(m.pendingMsg) != "" {
			m.addMsg(m.pendingMsg)
		}
		m.finishPrompt([]string{msg.command}, msg.res.ExitCode)
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	if m.pendingDone {
		// engine backstop for rule 7: one enforced verification turn even
		// though the model said done — the next message demands it
		m.enforceOnce = false
		m.pendingDone = false
		m.addMeta("(verification required — confirming the write first)")
		m.render()
	}
	m.turn++
	if m.turn >= m.cfg.Defaults.MaxTurns {
		m.addMeta("(max-turns reached)")
		m.finishPrompt([]string{msg.command}, msg.res.ExitCode)
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	m.m = modeStreaming
	m.render()
	m.startLLMTurnContinue()
}

func (m *replModel) startLLMTurnContinue() {
	// continue same prompt lifecycle (turn already incremented)
	m.raw = ""
	m.curOut = nil
	th := &ThoughtBlock{StartedAt: time.Now(), Expanded: m.cfg.UI.Verbose}
	m.add(&rBlock{kind: rThought, thought: th})
	m.render()
	baseURL, model, key, _ := m.llmReq()
	if key == "" {
		th.Done, th.Elapsed, th.Text = true, 0, "(no key)"
		m.addErr("no API key — get a free Groq key at " + llm.GroqKeyURL + ", then `argon settings`")
		m.m = modeInput
		m.input.Focus()
		m.render()
		return
	}
	m.status = fmt.Sprintf("turn %d…", m.turn+1)
	llmTimeout := time.Duration(m.cfg.Defaults.LLMTimeoutSec) * time.Second
	if llmTimeout <= 0 {
		llmTimeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	m.cancel = cancel
	if m.prog != nil {
		m.prog.Send(tickMsg{}) // kick live Thinking… counter
	}
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

func (m *replModel) nextTurn() {
	m.turn++
	if m.turn >= m.cfg.Defaults.MaxTurns {
		m.addMeta("(max-turns reached)")
		m.finishPrompt(nil, 0)
		m.m = modeInput
		m.input.Focus()
		return
	}
	m.userCtx = m.buildCtx("(directory changed, no output)", 0, true)
	m.startLLMTurnContinue()
}

func (m *replModel) finishPrompt(_ []string, exit int) {
	m.m = modeInput
	m.status = ""
	m.input.Focus()
	_ = history.Append("repl", m.prompt, m.taskCmds, m.taskOuts, exit, m.cfg.History.RememberOutputChars, m.cfg.History.FileMax)
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
	} else {
		b.WriteString(styleDim(m.cfg.UI.Color, "click a thought/output to expand · F2 toggles last"))
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
